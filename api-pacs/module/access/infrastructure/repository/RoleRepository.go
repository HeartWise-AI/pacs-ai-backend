package repository

import (
	"context"
	"errors"
	"log"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"api-pacs/infrastructures/providers/sdk/firebaseadmin"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/access/domain/entity"
	domainRepository "api-pacs/module/access/domain/repository"
	iamEntity "api-pacs/module/iam/domain/entity"
)

const usersCollection = "users"

// RoleRepository stores custom roles in Firestore `tenant_roles` and the
// assignment in `users.custom_role_id`.
type RoleRepository struct {
	FirebaseAdminSDK *firebaseadmin.FirebaseAdminSDK
}

type userRoleDocument struct {
	TenantID     string `firestore:"tenant_id"`
	Role         string `firestore:"role"`
	CustomRoleID string `firestore:"custom_role_id"`
}

func (repository *RoleRepository) client(ctx context.Context) (*firestore.Client, error) {
	if repository.FirebaseAdminSDK == nil || repository.FirebaseAdminSDK.App == nil {
		return nil, errors.New(apiError.FirestoreError)
	}
	client, err := repository.FirebaseAdminSDK.App.Firestore(ctx)
	if err != nil {
		log.Println(err)
		return nil, errors.New(apiError.FirestoreError)
	}
	return client, nil
}

// ListRoles returns every custom role of the tenant.
func (repository *RoleRepository) ListRoles(ctx context.Context, tenantID string) ([]entity.CustomRole, error) {
	client, err := repository.client(ctx)
	if err != nil {
		return nil, err
	}
	snapshots, err := client.Collection(entity.CustomRole{}.GetModelName()).Where("tenant_id", "==", tenantID).Documents(ctx).GetAll()
	if err != nil {
		log.Println(err)
		return nil, errors.New(apiError.FirestoreError)
	}
	roles := make([]entity.CustomRole, 0, len(snapshots))
	for _, snapshot := range snapshots {
		var role entity.CustomRole
		if err := snapshot.DataTo(&role); err != nil {
			log.Println(err)
			return nil, errors.New(apiError.FirestoreError)
		}
		if role.TenantID != tenantID {
			continue
		}
		role.ID = snapshot.Ref.ID
		roles = append(roles, role)
	}
	return roles, nil
}

// GetRole returns one custom role of the tenant or MissingRecord.
func (repository *RoleRepository) GetRole(ctx context.Context, tenantID, roleID string) (entity.CustomRole, error) {
	if roleID == "" {
		return entity.CustomRole{}, errors.New(apiError.MissingRecord)
	}
	client, err := repository.client(ctx)
	if err != nil {
		return entity.CustomRole{}, err
	}
	snapshot, err := client.Collection(entity.CustomRole{}.GetModelName()).Doc(roleID).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return entity.CustomRole{}, errors.New(apiError.MissingRecord)
		}
		log.Println(err)
		return entity.CustomRole{}, errors.New(apiError.FirestoreError)
	}
	var role entity.CustomRole
	if err := snapshot.DataTo(&role); err != nil {
		log.Println(err)
		return entity.CustomRole{}, errors.New(apiError.FirestoreError)
	}
	if role.TenantID != tenantID {
		return entity.CustomRole{}, errors.New(apiError.MissingRecord)
	}
	role.ID = snapshot.Ref.ID
	return role, nil
}

// CreateRole stores a new custom role document.
func (repository *RoleRepository) CreateRole(ctx context.Context, role entity.CustomRole) error {
	if role.ID == "" || role.TenantID == "" {
		return errors.New(apiError.InvalidPayload)
	}
	client, err := repository.client(ctx)
	if err != nil {
		return err
	}
	if _, err := client.Collection(role.GetModelName()).Doc(role.ID).Create(ctx, role); err != nil {
		if status.Code(err) == codes.AlreadyExists {
			return errors.New(apiError.DuplicateRecord)
		}
		log.Println(err)
		return errors.New(apiError.FirestoreError)
	}
	return nil
}

// readRoleInTransaction loads a role of the tenant inside a transaction.
func readRoleInTransaction(transaction *firestore.Transaction, ref *firestore.DocumentRef, tenantID string) (entity.CustomRole, error) {
	snapshot, err := transaction.Get(ref)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return entity.CustomRole{}, errors.New(apiError.MissingRecord)
		}
		return entity.CustomRole{}, err
	}
	var role entity.CustomRole
	if err := snapshot.DataTo(&role); err != nil {
		return entity.CustomRole{}, err
	}
	if role.TenantID != tenantID {
		return entity.CustomRole{}, errors.New(apiError.MissingRecord)
	}
	role.ID = snapshot.Ref.ID
	return role, nil
}

// runTransaction maps transaction errors: business codes pass through,
// anything else becomes FirestoreError.
func runTransaction(ctx context.Context, client *firestore.Client, body func(*firestore.Transaction) error) error {
	err := client.RunTransaction(ctx, func(_ context.Context, transaction *firestore.Transaction) error { return body(transaction) })
	if err == nil {
		return nil
	}
	switch err.Error() {
	case apiError.MissingRecord, apiError.InvalidPayload, apiError.RoleInUse:
		return err
	}
	log.Println(err)
	return errors.New(apiError.FirestoreError)
}

// UpdateRole replaces an existing custom role of the tenant. It never
// recreates a role deleted meanwhile.
func (repository *RoleRepository) UpdateRole(ctx context.Context, role entity.CustomRole) error {
	if role.ID == "" || role.TenantID == "" {
		return errors.New(apiError.InvalidPayload)
	}
	client, err := repository.client(ctx)
	if err != nil {
		return err
	}
	ref := client.Collection(role.GetModelName()).Doc(role.ID)
	return runTransaction(ctx, client, func(transaction *firestore.Transaction) error {
		if _, err := readRoleInTransaction(transaction, ref, role.TenantID); err != nil {
			return err
		}
		return transaction.Set(ref, role)
	})
}

// DeleteRole removes a custom role of the tenant that no user holds. The
// membership check and the delete run in one transaction that writes the
// role document; SetUserCustomRole writes it too, so a concurrent
// assignment and deletion cannot both succeed.
func (repository *RoleRepository) DeleteRole(ctx context.Context, tenantID, roleID string) error {
	if roleID == "" {
		return errors.New(apiError.MissingRecord)
	}
	client, err := repository.client(ctx)
	if err != nil {
		return err
	}
	ref := client.Collection(entity.CustomRole{}.GetModelName()).Doc(roleID)
	members := client.Collection(usersCollection).Where("tenant_id", "==", tenantID).Where("custom_role_id", "==", roleID).Limit(1)
	return runTransaction(ctx, client, func(transaction *firestore.Transaction) error {
		if _, err := readRoleInTransaction(transaction, ref, tenantID); err != nil {
			return err
		}
		held, err := transaction.Documents(members).GetAll()
		if err != nil {
			return err
		}
		if len(held) > 0 {
			return errors.New(apiError.RoleInUse)
		}
		return transaction.Delete(ref)
	})
}

// GetUserAssignment reads the role fields of one tenant user.
func (repository *RoleRepository) GetUserAssignment(ctx context.Context, tenantID, userID string) (domainRepository.UserRoleAssignment, error) {
	if userID == "" {
		return domainRepository.UserRoleAssignment{}, errors.New(apiError.MissingRecord)
	}
	client, err := repository.client(ctx)
	if err != nil {
		return domainRepository.UserRoleAssignment{}, err
	}
	snapshot, err := client.Collection(usersCollection).Doc(userID).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return domainRepository.UserRoleAssignment{}, errors.New(apiError.MissingRecord)
		}
		log.Println(err)
		return domainRepository.UserRoleAssignment{}, errors.New(apiError.FirestoreError)
	}
	var document userRoleDocument
	if err := snapshot.DataTo(&document); err != nil {
		log.Println(err)
		return domainRepository.UserRoleAssignment{}, errors.New(apiError.FirestoreError)
	}
	if document.TenantID != tenantID {
		return domainRepository.UserRoleAssignment{}, errors.New(apiError.MissingRecord)
	}
	return domainRepository.UserRoleAssignment{
		UserID: snapshot.Ref.ID, TenantID: document.TenantID, Role: document.Role, CustomRoleID: document.CustomRoleID,
	}, nil
}

// ListUserAssignments reads the role fields of every tenant user.
func (repository *RoleRepository) ListUserAssignments(ctx context.Context, tenantID string) ([]domainRepository.UserRoleAssignment, error) {
	client, err := repository.client(ctx)
	if err != nil {
		return nil, err
	}
	snapshots, err := client.Collection(usersCollection).Where("tenant_id", "==", tenantID).Documents(ctx).GetAll()
	if err != nil {
		log.Println(err)
		return nil, errors.New(apiError.FirestoreError)
	}
	assignments := make([]domainRepository.UserRoleAssignment, 0, len(snapshots))
	for _, snapshot := range snapshots {
		var document userRoleDocument
		if err := snapshot.DataTo(&document); err != nil {
			log.Println(err)
			return nil, errors.New(apiError.FirestoreError)
		}
		if document.TenantID != tenantID {
			continue
		}
		assignments = append(assignments, domainRepository.UserRoleAssignment{
			UserID: snapshot.Ref.ID, TenantID: document.TenantID, Role: document.Role, CustomRoleID: document.CustomRoleID,
		})
	}
	return assignments, nil
}

// SetUserCustomRole stores the custom role of a tenant user ("" clears it).
// Assigning reads and touches the role document in the same transaction as
// the user write, so it cannot interleave with DeleteRole: one of the two
// fails (MissingRecord or ROLE_IN_USE) instead of leaving a dangling role.
func (repository *RoleRepository) SetUserCustomRole(ctx context.Context, tenantID, userID, roleID string) error {
	if userID == "" {
		return errors.New(apiError.MissingRecord)
	}
	client, err := repository.client(ctx)
	if err != nil {
		return err
	}
	userRef := client.Collection(usersCollection).Doc(userID)
	roleRef := client.Collection(entity.CustomRole{}.GetModelName()).Doc(roleID)
	return runTransaction(ctx, client, func(transaction *firestore.Transaction) error {
		if roleID != "" {
			if _, err := readRoleInTransaction(transaction, roleRef, tenantID); err != nil {
				return err
			}
		}
		snapshot, err := transaction.Get(userRef)
		if err != nil {
			if status.Code(err) == codes.NotFound {
				return errors.New(apiError.MissingRecord)
			}
			return err
		}
		var document userRoleDocument
		if err := snapshot.DataTo(&document); err != nil {
			return err
		}
		if document.TenantID != tenantID {
			return errors.New(apiError.MissingRecord)
		}
		if roleID != "" && document.Role != iamEntity.UserRole {
			return errors.New(apiError.InvalidPayload)
		}
		now := time.Now().Unix()
		if roleID != "" {
			if err := transaction.Update(roleRef, []firestore.Update{{Path: "last_assigned_at", Value: now}}); err != nil {
				return err
			}
		}
		return transaction.Update(userRef, []firestore.Update{
			{Path: "custom_role_id", Value: roleID},
			{Path: "updated_at", Value: int(now)},
		})
	})
}
