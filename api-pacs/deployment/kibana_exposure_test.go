package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/docker/go-connections/nat"
	esindex "github.com/elastic/go-elasticsearch/v8/typedapi/core/index"
	"github.com/stretchr/testify/require"

	esTypes "api-pacs/infrastructures/database/elasticsearch/types"
	esRepository "api-pacs/module/elasticsearch/infrastructure/repository"
	repositoryTypes "api-pacs/module/elasticsearch/infrastructure/repository/types"
)

func TestShippedStacksDoNotExposeUnauthenticatedAuditCluster(t *testing.T) {
	root := repoRoot(t)
	stacks := []string{
		filepath.Join(root, "docker-compose.yml"),
		filepath.Join(root, "docker-compose-dev.yml"),
	}

	sawAuditStack := false
	for _, stackPath := range stacks {
		included := includedComposeFiles(t, stackPath)
		for _, rel := range included {
			composePath := filepath.Join(filepath.Dir(stackPath), rel)
			if _, err := os.Stat(composePath); err != nil {
				continue
			}
			services := parseComposeServices(t, composePath)
			if services["elasticsearch"] == nil && services["kibana"] == nil {
				continue
			}
			sawAuditStack = true
			t.Run(rel, func(t *testing.T) {
				store := seedAuditIndices(t)
				client := anonymousClusterClient{services: services, store: store}
				for _, indexName := range store.indexNames() {
					documents, searchErr := client.Search(indexName)
					deleteErr := client.Delete(indexName)
					require.Error(t, searchErr, "%s publishes %s to an anonymous client: %s", rel, indexName, documents)
					require.Error(t, deleteErr, "%s lets an anonymous client delete %s", rel, indexName)
					require.NotEmpty(t, store.documents(indexName), "%s dropped %s before refusing the anonymous delete", rel, indexName)
				}
			})
		}
	}
	require.True(t, sawAuditStack, "deployment compose no longer includes the elasticsearch stack under test")
}

type anonymousClusterClient struct {
	services map[string]*composeService
	store    *auditIndex
}

func (client anonymousClusterClient) Search(indexName string) (string, error) {
	if err := client.authorize(); err != nil {
		return "", err
	}
	return string(client.store.joined(indexName)), nil
}

func (client anonymousClusterClient) Delete(indexName string) error {
	if err := client.authorize(); err != nil {
		return err
	}
	client.store.remove(indexName)
	return nil
}

func (client anonymousClusterClient) authorize() error {
	elasticsearch := client.services["elasticsearch"]
	if elasticsearch == nil || elasticsearchSecurityEnabled(elasticsearch.environment) {
		return errors.New("elasticsearch security requires authentication")
	}
	if !publishedBeyondLoopback(elasticsearch.ports) && !publishedBeyondLoopback(client.servicePorts("kibana")) {
		return errors.New("audit cluster is not published beyond loopback")
	}
	return nil
}

func (client anonymousClusterClient) servicePorts(name string) []string {
	service := client.services[name]
	if service == nil {
		return nil
	}
	return service.ports
}

func elasticsearchSecurityEnabled(environment []string) bool {
	enabled := true
	for _, item := range environment {
		key, value, ok := strings.Cut(item, "=")
		if ok && key == "xpack.security.enabled" {
			enabled = strings.EqualFold(strings.TrimSpace(value), "true")
		}
	}
	return enabled
}

func publishedBeyondLoopback(specs []string) bool {
	for _, spec := range specs {
		mappings, err := nat.ParsePortSpec(spec)
		if err != nil {
			return true
		}
		for _, mapping := range mappings {
			switch mapping.Binding.HostIP {
			case "", "0.0.0.0", "::":
				return true
			}
		}
	}
	return false
}

type composeService struct {
	environment []string
	ports       []string
}

func parseComposeServices(t *testing.T, path string) map[string]*composeService {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)

	services := map[string]*composeService{}
	var current *composeService
	var section string
	inServices := false
	for _, line := range strings.Split(string(content), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		text := stripYAMLComment(strings.TrimSpace(line))
		if text == "" {
			continue
		}
		if indent == 0 {
			inServices = text == "services:"
			current = nil
			section = ""
			continue
		}
		if !inServices {
			continue
		}
		if indent == 2 && strings.HasSuffix(text, ":") && !strings.HasPrefix(text, "-") {
			current = &composeService{}
			services[strings.TrimSuffix(text, ":")] = current
			section = ""
			continue
		}
		if current == nil {
			continue
		}
		if indent == 4 && strings.HasSuffix(text, ":") && !strings.HasPrefix(text, "-") {
			section = strings.TrimSuffix(text, ":")
			continue
		}
		if !strings.HasPrefix(text, "- ") {
			if section == "environment" && strings.Contains(text, ":") {
				key, value, _ := strings.Cut(text, ":")
				current.environment = append(current.environment, strings.TrimSpace(key)+"="+unquote(strings.TrimSpace(value)))
			}
			continue
		}
		value := unquote(strings.TrimSpace(strings.TrimPrefix(text, "- ")))
		switch section {
		case "environment":
			current.environment = append(current.environment, value)
		case "ports":
			current.ports = append(current.ports, value)
		}
	}
	return services
}

func includedComposeFiles(t *testing.T, path string) []string {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)

	var included []string
	inInclude := false
	for _, line := range strings.Split(string(content), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		text := stripYAMLComment(strings.TrimSpace(line))
		if indent == 0 {
			inInclude = text == "include:"
			continue
		}
		if inInclude && strings.HasPrefix(text, "- ") {
			included = append(included, unquote(strings.TrimSpace(strings.TrimPrefix(text, "- "))))
		}
	}
	require.NotEmpty(t, included, path)
	return included
}

func stripYAMLComment(value string) string {
	quoted := false
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '"', '\'':
			quoted = !quoted
		case '#':
			if !quoted {
				return strings.TrimSpace(value[:i])
			}
		}
	}
	return strings.TrimSpace(value)
}

func unquote(value string) string {
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "elasticsearch", "docker-compose.yml")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, parent, dir)
		dir = parent
	}
}

type auditIndex struct {
	esTypes.ElasticsearchDBHandlerInterface
	docs map[string][][]byte
}

func (store *auditIndex) IndexDocument(_ context.Context, name string, document interface{}) (*esindex.Response, error) {
	raw, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	store.docs[name] = append(store.docs[name], raw)
	return &esindex.Response{}, nil
}

func (index *auditIndex) indexNames() []string {
	names := make([]string, 0, len(index.docs))
	for name := range index.docs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (index *auditIndex) documents(name string) [][]byte {
	return index.docs[name]
}

func (index *auditIndex) joined(name string) []byte {
	return append([]byte(nil), bytesJoin(index.docs[name])...)
}

func (index *auditIndex) remove(name string) {
	delete(index.docs, name)
}

func bytesJoin(parts [][]byte) []byte {
	var out []byte
	for _, part := range parts {
		out = append(out, part...)
	}
	return out
}

func seedAuditIndices(t *testing.T) *auditIndex {
	t.Helper()
	store := &auditIndex{docs: map[string][][]byte{}}
	repository := &esRepository.ElasticsearchCommandRepository{ElasticsearchDBHandlerInterface: store}
	ctx := context.Background()

	_, err := repository.InsertLoginLog(ctx, repositoryTypes.CreateLoginLog{
		SessionID: "audit-session", TenantID: "tenant-a", TenantName: "Example Workspace",
		UserID: "user-owner", Email: "owner@example.com", Name: "Owner Example", Role: "OWNER", Specialty: "cardiology",
	})
	require.NoError(t, err)
	_, err = repository.InsertStoredCustomSeriesLog(ctx, repositoryTypes.CreateStoredCustomSeriesLog{
		TenantID: "tenant-a", TenantName: "Example Workspace", UserID: "user-owner", Email: "owner@example.com",
		Name: "Owner Example", ModalityID: "modality-a", StudyInstanceUID: "1.2.3.4.5",
		SeriesInstanceUIDs: []string{"1.2.3.4.5.6"}, PatientID: "SYNTH-PATIENT",
		ModelName: "example-model", ModelVersion: "1", CustomSeriesInstanceUID: "1.2.3.4.5.7", CustomSOPInstanceUID: "1.2.3.4.5.8",
	})
	require.NoError(t, err)
	_, err = repository.InsertPredictInferenceModelLog(ctx, repositoryTypes.CreatePredictInferenceModelLog{
		TenantID: "tenant-a", TenantName: "Example Workspace", UserID: "user-owner", Email: "owner@example.com",
		Name: "Owner Example", ContainerID: "container-a", ContainerName: "model-a", InferenceModelID: "model-a",
		InferenceModelName: "Example", DockerImage: "example/model:1", Model: "example",
		StudyInstanceUID: "1.2.3.4.5", SeriesInstanceUIDs: []string{"1.2.3.4.5.6"},
	})
	require.NoError(t, err)
	_, err = repository.InsertRetrieveStudyLog(ctx, repositoryTypes.CreateRetrieveStudyLog{
		TenantID: "tenant-a", TenantName: "Example Workspace", ModalityID: "modality-a",
		UserID: "user-owner", Email: "owner@example.com", Name: "Owner Example", StudyInstanceUID: "1.2.3.4.5",
	})
	require.NoError(t, err)
	_, err = repository.InsertGetModalityStudyLog(ctx, repositoryTypes.CreateGetModalityStudyLog{
		TenantID: "tenant-a", TenantName: "Example Workspace", ModalityID: "modality-a",
		UserID: "user-owner", Email: "owner@example.com", Name: "Owner Example", QueryID: "query-a",
	})
	require.NoError(t, err)
	_, err = repository.InsertAdminInviteLog(ctx, repositoryTypes.CreateAdminInviteLog{
		TenantID: "tenant-a", TenantName: "Example Workspace", Email: "invitee@example.com",
	})
	require.NoError(t, err)
	_, err = repository.InsertAdminMemberLog(ctx, repositoryTypes.CreateAdminMemberLog{
		TenantID: "tenant-a", TenantName: "Example Workspace", UserID: "user-owner", Email: "owner@example.com",
		Name: "Owner Example", Role: "OWNER", Action: "CREATE",
	})
	require.NoError(t, err)
	_, err = repository.InsertSignedConsentLog(ctx, repositoryTypes.CreateSignedConsentLog{
		TenantID: "tenant-a", TenantName: "Example Workspace", UserID: "user-owner", Email: "owner@example.com",
	})
	require.NoError(t, err)
	require.NotEmpty(t, store.documents("logins"))
	require.NotEmpty(t, store.documents("stored_custom_series"))
	return store
}
