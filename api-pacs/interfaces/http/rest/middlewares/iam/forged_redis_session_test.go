package iam

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	goredis "github.com/go-redis/redis"
	"github.com/stretchr/testify/require"

	redisdb "api-pacs/infrastructures/database/redis"
	middlewaretypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	accessentity "api-pacs/module/access/domain/entity"
	accesssvc "api-pacs/module/access/infrastructure/service"
	iamentity "api-pacs/module/iam/domain/entity"
	iamrepo "api-pacs/module/iam/infrastructure/repository"
	iamsvc "api-pacs/module/iam/infrastructure/service"
	iamsvctypes "api-pacs/module/iam/infrastructure/service/types"
	userapp "api-pacs/module/user/application"
	usertypes "api-pacs/module/user/infrastructure/service/types"
)

// documentedIAMRedisDB is REDIS_IAM_DB in api-pacs/.env.example and the README.
const documentedIAMRedisDB = 1

// modelContainerNetwork is the Docker network model containers join. The root
// compose file names its default network this, and CreateContainer attaches
// model containers to DOCKER_NETWORK, which deployment docs set to this name.
const modelContainerNetwork = "pacs-net"

func TestServerIssuedSessionAuthorizesAndRawRedisWriteDoesNot(t *testing.T) {
	addr, password := startRedisFromShippedConfig(t)
	rejectUnauthenticatedRedis(t, addr)

	handler := &redisdb.RedisDBHandler{}
	_, err := handler.Connect(addr, password, documentedIAMRedisDB)
	require.NoError(t, err)
	t.Cleanup(func() { _ = handler.Client.Close() })

	queryRepo := &iamrepo.IAMQueryRepository{RedisDBHandlerInterface: handler}
	commandRepo := &iamrepo.IAMCommandRepository{RedisDBHandlerInterface: handler}
	middleware := &IAMMiddleware{
		IAMQueryServiceInterface: &iamsvc.IAMQueryService{
			IAMQueryRepositoryInterface: &iamrepo.IAMQueryRepositoryCircuitBreaker{
				IAMQueryRepositoryInterface: queryRepo,
			},
		},
		IAMCommandServiceInterface: &iamsvc.IAMCommandService{
			IAMCommandRepositoryInterface: &iamrepo.IAMCommandRepositoryCircuitBreaker{
				IAMCommandRepositoryInterface: commandRepo,
			},
		},
		AccessResolver: &accesssvc.AccessService{},
	}

	serverToken := "server-issued-session"
	require.NoError(t, middleware.IAMCommandServiceInterface.SetTokenSession(context.Background(), iamsvctypes.SetTokenSession{
		SessionID: serverToken,
		TenantID:  "tenant-server",
		UserID:    "user-server",
		Role:      iamentity.UserRole,
	}))
	code, role, reached := roundTrip(t, middleware.TokenSessionAuthGuard, serverToken)
	if code != http.StatusOK || !reached || role != iamentity.UserRole {
		t.Fatalf("server-issued session was not accepted (status %d, reached %v, role %q); the harness is not exercising the live session store", code, reached, role)
	}

	attacker := goredis.NewClient(&goredis.Options{Addr: addr, Password: password, DB: documentedIAMRedisDB})
	t.Cleanup(func() { _ = attacker.Close() })

	for _, roleName := range []string{iamentity.OwnerRole, iamentity.AdminRole, iamentity.UserRole} {
		token := "forged-" + strings.ToLower(roleName)
		payload, err := json.Marshal(iamentity.TokenSession{
			TenantID:   "tenant-victim",
			UserID:     "user-" + strings.ToLower(roleName),
			Role:       roleName,
			Generation: iamentity.SessionGeneration,
		})
		require.NoError(t, err)
		require.NoError(t, attacker.Set(token, string(payload), time.Minute).Err())

		t.Run("token-guard-"+roleName, func(t *testing.T) {
			assertForgedSessionRejected(t, middleware.TokenSessionAuthGuard, token)
		})
		t.Run("orthanc-proxy-guard-"+roleName, func(t *testing.T) {
			assertForgedSessionRejected(t, middleware.TokenSessionOrthancProxyAuthGuard, token)
		})
	}

	ownerToken := "forged-" + strings.ToLower(iamentity.OwnerRole)
	ownerGuards := []struct {
		name  string
		guard func(http.Handler) http.Handler
	}{
		{"logs", middleware.RequirePermission(accessentity.PermLogsView)},
		{"members", middleware.RequirePermission(accessentity.PermMembersManage)},
		{"owner", middleware.RBACOwnerGuard},
		{"owner-or-admin", middleware.RBACOwnerOrAdminGuard},
		{"dicom-accepted-policy", chain(policyGuard(middleware, false), middleware.RequirePermission(accessentity.PermDicomManage))},
		{"dicom-policy-required", chain(policyGuard(middleware, true), middleware.RequirePermission(accessentity.PermDicomManage))},
	}
	for _, entry := range ownerGuards {
		t.Run(entry.name, func(t *testing.T) {
			assertForgedSessionRejected(t, func(next http.Handler) http.Handler {
				return middleware.TokenSessionAuthGuard(entry.guard(next))
			}, ownerToken)
		})
	}
}

func TestRedisConfDoesNotCommitRequirepass(t *testing.T) {
	if _, ok := committedRequirepass(t, filepath.Join(repoRoot(t), "redis", "redis.conf")); ok {
		t.Fatal("redis/redis.conf commits a requirepass value")
	}
}

func TestReadmeDoesNotPrescribeRedisPassword(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "README.md"))
	require.NoError(t, err)
	for _, line := range strings.Split(string(body), "\n") {
		if strings.Contains(line, "REDIS_PASSWORD") && strings.Contains(line, "set to `") {
			t.Fatal("README.md prescribes a fixed REDIS_PASSWORD")
		}
	}
}

func TestRedisServiceIsNotOnModelNetwork(t *testing.T) {
	root := repoRoot(t)
	if redisAttachedTo(t, root, modelContainerNetwork) {
		t.Fatal("redis is attached to the model container network")
	}
}

func assertForgedSessionRejected(t *testing.T, guard func(http.Handler) http.Handler, token string) {
	t.Helper()
	code, role, reached := roundTrip(t, guard, token)
	if code != http.StatusUnauthorized || reached {
		t.Fatalf("raw Redis session was authorized (status %d, reached %v, role %q)", code, reached, role)
	}
}

func roundTrip(t *testing.T, guard func(http.Handler) http.Handler, token string) (int, string, bool) {
	t.Helper()
	var reached bool
	var role string
	handler := guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		role, _ = r.Context().Value(middlewaretypes.RoleCtx).(string)
		w.WriteHeader(http.StatusOK)
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder.Code, role, reached
}

func chain(outer, inner func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return outer(inner(next))
	}
}

type fixedPolicyQuery struct {
	userapp.UserQueryServiceInterface
	acceptanceRequired bool
}

func (query fixedPolicyQuery) GetPolicyStatus(context.Context, string, string) (usertypes.PolicyStatus, error) {
	return usertypes.PolicyStatus{EnforcementActive: true, AcceptanceRequired: query.acceptanceRequired}, nil
}

func policyGuard(middleware *IAMMiddleware, acceptanceRequired bool) func(http.Handler) http.Handler {
	copy := *middleware
	copy.UserQueryServiceInterface = fixedPolicyQuery{acceptanceRequired: acceptanceRequired}
	return copy.PolicyAcceptanceGuard
}

func rejectUnauthenticatedRedis(t *testing.T, addr string) {
	t.Helper()
	client := goredis.NewClient(&goredis.Options{Addr: addr, DB: documentedIAMRedisDB})
	defer client.Close()
	if err := client.Ping().Err(); err == nil {
		t.Fatal("redis accepted a connection without its configured password")
	}
}

func startRedisFromShippedConfig(t *testing.T) (string, string) {
	t.Helper()
	binary, err := exec.LookPath("redis-server")
	require.NoError(t, err)

	confPath := filepath.Join(repoRoot(t), "redis", "redis.conf")
	password, ok := committedRequirepass(t, confPath)
	if !ok {
		password = "placeholder-not-a-secret"
	}

	dir := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())

	shipped, err := os.ReadFile(confPath)
	require.NoError(t, err)
	runtimeConf := filepath.Join(dir, "redis.conf")
	var builder strings.Builder
	builder.Write(shipped)
	if !ok {
		builder.WriteString("\nrequirepass placeholder-not-a-secret\n")
	}
	builder.WriteString("\nbind 127.0.0.1\nport ")
	builder.WriteString(strconv.Itoa(port))
	builder.WriteString("\ndir ")
	builder.WriteString(dir)
	builder.WriteString("\nappendonly no\nsave \"\"\n")
	require.NoError(t, os.WriteFile(runtimeConf, []byte(builder.String()), 0o600))

	command := exec.Command(binary, runtimeConf)
	command.Dir = dir
	output, err := os.Create(filepath.Join(dir, "redis.log"))
	require.NoError(t, err)
	command.Stdout = output
	command.Stderr = output
	require.NoError(t, command.Start())
	t.Cleanup(func() {
		if command.Process != nil {
			_ = command.Process.Kill()
			_, _ = command.Process.Wait()
		}
		_ = output.Close()
	})

	addr := "127.0.0.1:" + strconv.Itoa(port)
	deadline := time.Now().Add(10 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		client := goredis.NewClient(&goredis.Options{Addr: addr, Password: password, DB: documentedIAMRedisDB})
		last = client.Ping().Err()
		_ = client.Close()
		if last == nil {
			return addr, password
		}
		time.Sleep(50 * time.Millisecond)
	}
	logged, _ := os.ReadFile(filepath.Join(dir, "redis.log"))
	t.Fatalf("redis did not accept its configured password: %v\n%s", last, redactConfiguredPassword(string(logged), password))
	return "", ""
}

func committedRequirepass(t *testing.T, path string) (string, bool) {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	var password string
	found := false
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) >= 2 && strings.EqualFold(fields[0], "requirepass") {
			password = fields[1]
			found = true
		}
	}
	return password, found
}

func redactConfiguredPassword(text, password string) string {
	if password == "" {
		return text
	}
	return strings.ReplaceAll(text, password, "[redacted]")
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "redis", "redis.conf")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "docker-compose.yml")); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repository root not found")
		}
		dir = parent
	}
}

func redisAttachedTo(t *testing.T, root, network string) bool {
	t.Helper()
	names, present := redisServiceNetworks(t, filepath.Join(root, "redis", "docker-compose.yml"))
	defaultName := composeDefaultNetwork(t, filepath.Join(root, "docker-compose.yml"))
	if !present {
		return defaultName == network
	}
	for _, name := range names {
		if name == network || (name == "default" && defaultName == network) {
			return true
		}
	}
	return false
}

func redisServiceNetworks(t *testing.T, path string) ([]string, bool) {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	lines := strings.Split(string(body), "\n")
	inService := false
	inNetworks := false
	present := false
	var names []string
	for _, line := range lines {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent == 2 && strings.HasPrefix(strings.TrimSpace(line), "redis:") {
			inService = true
			inNetworks = false
			continue
		}
		if inService && indent <= 2 {
			break
		}
		if !inService {
			continue
		}
		if indent == 4 && strings.HasPrefix(strings.TrimSpace(line), "networks:") {
			inNetworks = true
			present = true
			continue
		}
		if inNetworks && indent == 6 && strings.HasPrefix(strings.TrimSpace(line), "-") {
			names = append(names, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-")))
			continue
		}
		if inNetworks && indent <= 4 {
			inNetworks = false
		}
	}
	return names, present
}

func composeDefaultNetwork(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	lines := strings.Split(string(body), "\n")
	inNetworks := false
	inDefault := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent == 0 && trimmed == "networks:" {
			inNetworks = true
			inDefault = false
			continue
		}
		if inNetworks && indent == 0 {
			inNetworks = false
		}
		if !inNetworks {
			continue
		}
		if indent == 2 && trimmed == "default:" {
			inDefault = true
			continue
		}
		if inDefault && indent == 2 {
			inDefault = false
		}
		if inDefault && indent == 4 && strings.HasPrefix(trimmed, "name:") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "name:"))
		}
	}
	return ""
}
