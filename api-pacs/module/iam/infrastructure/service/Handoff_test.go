package service

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
	iamRepository "api-pacs/module/iam/infrastructure/repository"
	"api-pacs/module/iam/infrastructure/service/types"
	userServiceTypes "api-pacs/module/user/infrastructure/service/types"
)

type memoryCodes struct {
	values  map[string]string
	ttls    map[string]time.Duration
	counter map[string]int64
	fail    error
}

func newMemoryCodes() *memoryCodes {
	return &memoryCodes{values: map[string]string{}, ttls: map[string]time.Duration{}, counter: map[string]int64{}}
}

func (m *memoryCodes) SetIfAbsent(key string, val interface{}, exp time.Duration) (bool, error) {
	if m.fail != nil {
		return false, m.fail
	}
	if _, ok := m.values[key]; ok {
		return false, nil
	}
	m.values[key] = val.(string)
	m.ttls[key] = exp
	return true, nil
}

func (m *memoryCodes) GetAndDelete(key string) (string, bool, error) {
	value, ok := m.values[key]
	delete(m.values, key)
	return value, ok, nil
}

func (m *memoryCodes) IncrementWithExpiry(key string, _ time.Duration) (int64, time.Duration, error) {
	m.counter[key]++
	return m.counter[key], time.Minute, nil
}

type memoryLinks struct {
	links []iamRepository.AccountLink
	saved []iamRepository.AccountLink
}

func (m *memoryLinks) FindActiveAccountLink(_ context.Context, provider, tenantID, externalUID string) (iamRepository.AccountLink, bool, error) {
	for _, link := range m.links {
		if link.Provider == provider && link.TenantID == tenantID && link.ExternalUID == externalUID {
			return link, true, nil
		}
	}
	return iamRepository.AccountLink{}, false, nil
}

func (m *memoryLinks) SaveAccountLink(_ context.Context, link iamRepository.AccountLink) error {
	kept := m.links[:0]
	for _, existing := range m.links {
		if existing.ExternalUID != link.ExternalUID && existing.PacsUserID != link.PacsUserID {
			kept = append(kept, existing)
		}
	}
	m.links = append(kept, link)
	m.saved = append(m.saved, link)
	return nil
}

type memoryUsers map[string]userServiceTypes.GetTenantUser

func (m memoryUsers) GetTenantUserByID(_ context.Context, tenantID, id string) (userServiceTypes.GetTenantUser, error) {
	user, ok := m[tenantID+"/"+id]
	if !ok {
		return userServiceTypes.GetTenantUser{}, errors.New("not found")
	}
	return user, nil
}

type memorySessions struct {
	created []types.SetTokenSession
	err     error
}

func (m *memorySessions) SetTokenSession(_ context.Context, data types.SetTokenSession) error {
	if m.err != nil {
		return m.err
	}
	m.created = append(m.created, data)
	return nil
}

type handoffFixture struct {
	service  *HandoffService
	codes    *memoryCodes
	links    *memoryLinks
	users    memoryUsers
	sessions *memorySessions
}

func newHandoffFixture() handoffFixture {
	f := handoffFixture{
		codes:    newMemoryCodes(),
		links:    &memoryLinks{},
		users:    memoryUsers{"t1/u1": {ID: "u1", TenantID: "t1", Role: "USER", Email: "doc@example.org", IsEmailVerified: true}},
		sessions: &memorySessions{},
	}
	f.service = &HandoffService{Codes: f.codes, Links: f.links, Users: f.users, Sessions: f.sessions, PublicURL: "https://pacs.example.org/"}
	return f
}

func mintRequest() MintHandoffRequest {
	return MintHandoffRequest{TenantID: "t1", ExternalUID: "fb-1", Email: "Doc@Example.org", EmailVerified: true, ReturnPath: "/viewer?StudyInstanceUIDs=1.2.3"}
}

func codeFromURL(t *testing.T, url, page string) string {
	t.Helper()
	prefix := "https://pacs.example.org/auth/" + page + "#code="
	require.True(t, strings.HasPrefix(url, prefix), url)
	code := strings.TrimPrefix(url, prefix)
	require.Len(t, code, 43)
	return code
}

func TestReturnPathAllowlist(t *testing.T) {
	for _, path := range []string{"/", "/viewer?StudyInstanceUIDs=1.2.3", "/viewer/dicomweb?StudyInstanceUIDs=1.2", "/worklist"} {
		require.True(t, ValidHandoffReturnPath(path), path)
	}
	for _, path := range []string{"", "viewer", "//evil.example", "https://evil.example/viewer", "/\\evil", "/admin", "/viewer#x", "/viewer\n", "/v1/iam/login"} {
		require.False(t, ValidHandoffReturnPath(path), path)
	}
}

func TestUnlinkedUserGetsALinkCodeNotASession(t *testing.T) {
	f := newHandoffFixture()

	result, err := f.service.MintHandoff(context.Background(), mintRequest())

	require.NoError(t, err)
	require.True(t, result.LinkRequired)
	require.Equal(t, 300, result.ExpiresIn)
	code := codeFromURL(t, result.URL, "link")
	// A link code cannot be exchanged for a session.
	_, err = f.service.ExchangeHandoff(context.Background(), code, "10.0.0.1")
	require.ErrorIs(t, err, ErrHandoffInvalidCode)
	require.Empty(t, f.sessions.created)
}

func TestCodesAreStoredOnlyAsHashes(t *testing.T) {
	f := newHandoffFixture()
	result, err := f.service.MintHandoff(context.Background(), mintRequest())
	require.NoError(t, err)
	code := codeFromURL(t, result.URL, "link")
	for key, value := range f.codes.values {
		require.NotContains(t, key, code)
		require.NotContains(t, value, code)
		require.Equal(t, 5*time.Minute, f.codes.ttls[key])
	}
}

func TestLinkThenHandoffOpensASessionOnce(t *testing.T) {
	f := newHandoffFixture()
	ctx := context.Background()
	linkResult, err := f.service.MintHandoff(ctx, mintRequest())
	require.NoError(t, err)

	returnPath, err := f.service.CompleteHandoffLink(ctx, codeFromURL(t, linkResult.URL, "link"), "t1", "u1")
	require.NoError(t, err)
	require.Equal(t, "/viewer?StudyInstanceUIDs=1.2.3", returnPath)
	require.Equal(t, iamRepository.AccountLink{Provider: "cardioagent", ExternalUID: "fb-1", TenantID: "t1", PacsUserID: "u1",
		EmailAtLink: "doc@example.org", LinkedBy: "self-password"}, f.links.saved[0])

	result, err := f.service.MintHandoff(ctx, mintRequest())
	require.NoError(t, err)
	require.False(t, result.LinkRequired)
	require.Equal(t, 60, result.ExpiresIn)
	code := codeFromURL(t, result.URL, "handoff")

	session, err := f.service.ExchangeHandoff(ctx, code, "10.0.0.1")
	require.NoError(t, err)
	require.NotEmpty(t, session.SessionToken)
	require.Equal(t, "t1", session.TenantID)
	require.Equal(t, "/viewer?StudyInstanceUIDs=1.2.3", session.ReturnPath)
	require.Equal(t, types.SetTokenSession{SessionID: session.SessionToken, TenantID: "t1", UserID: "u1", Role: "USER",
		ExpireTimeInSeconds: 900}, f.sessions.created[0])

	_, err = f.service.ExchangeHandoff(ctx, code, "10.0.0.1")
	require.ErrorIs(t, err, ErrHandoffInvalidCode, "a code works once")
}

func TestLinkCodeIsBoundToItsTenant(t *testing.T) {
	f := newHandoffFixture()
	f.users["t2/u9"] = userServiceTypes.GetTenantUser{ID: "u9", IsEmailVerified: true}
	result, _ := f.service.MintHandoff(context.Background(), mintRequest())

	_, err := f.service.CompleteHandoffLink(context.Background(), codeFromURL(t, result.URL, "link"), "t2", "u9")

	require.ErrorIs(t, err, ErrHandoffInvalidCode)
	require.Empty(t, f.links.saved)
}

func TestMintRejectsUnverifiedEmailBadInputAndBlockedUsers(t *testing.T) {
	f := newHandoffFixture()
	ctx := context.Background()

	unverified := mintRequest()
	unverified.EmailVerified = false
	_, err := f.service.MintHandoff(ctx, unverified)
	require.ErrorIs(t, err, ErrHandoffNotAllowed)

	badPath := mintRequest()
	badPath.ReturnPath = "https://evil.example"
	_, err = f.service.MintHandoff(ctx, badPath)
	require.ErrorIs(t, err, ErrHandoffInvalidRequest)

	noUID := mintRequest()
	noUID.ExternalUID = " "
	_, err = f.service.MintHandoff(ctx, noUID)
	require.ErrorIs(t, err, ErrHandoffInvalidRequest)

	f.links.links = []iamRepository.AccountLink{{Provider: "cardioagent", ExternalUID: "fb-1", TenantID: "t1", PacsUserID: "u1"}}
	f.users["t1/u1"] = userServiceTypes.GetTenantUser{ID: "u1", Email: "doc@example.org", IsEmailVerified: true, AccessState: "SUSPENDED"}
	_, err = f.service.MintHandoff(ctx, mintRequest())
	require.ErrorIs(t, err, ErrHandoffNotAllowed)
}

func TestExchangeRechecksLinkAndUser(t *testing.T) {
	f := newHandoffFixture()
	ctx := context.Background()
	f.links.links = []iamRepository.AccountLink{{Provider: "cardioagent", ExternalUID: "fb-1", TenantID: "t1", PacsUserID: "u1"}}

	result, _ := f.service.MintHandoff(ctx, mintRequest())
	f.links.links = nil // unlinked after minting
	_, err := f.service.ExchangeHandoff(ctx, codeFromURL(t, result.URL, "handoff"), "10.0.0.1")
	require.ErrorIs(t, err, ErrHandoffNotAllowed)

	f.links.links = []iamRepository.AccountLink{{Provider: "cardioagent", ExternalUID: "fb-1", TenantID: "t1", PacsUserID: "u1"}}
	result, _ = f.service.MintHandoff(ctx, mintRequest())
	f.sessions.err = errors.New(apiError.AccountSuspended) // suspended meanwhile
	_, err = f.service.ExchangeHandoff(ctx, codeFromURL(t, result.URL, "handoff"), "10.0.0.1")
	require.ErrorIs(t, err, ErrHandoffNotAllowed)
}

func TestExchangeRejectsMalformedCodesAndRateLimits(t *testing.T) {
	f := newHandoffFixture()
	for _, code := range []string{"", "short", strings.Repeat("a", 44), strings.Repeat("!", 43)} {
		_, err := f.service.ExchangeHandoff(context.Background(), code, "")
		require.ErrorIs(t, err, ErrHandoffInvalidCode, code)
	}
	var err error
	for i := 0; i < 31; i++ {
		_, err = f.service.ExchangeHandoff(context.Background(), strings.Repeat("a", 43), "10.0.0.9")
	}
	require.ErrorIs(t, err, ErrHandoffRateLimited)
}

func TestHandoffIsUnavailableUntilConfigured(t *testing.T) {
	f := newHandoffFixture()
	f.service.PublicURL = ""
	_, err := f.service.MintHandoff(context.Background(), mintRequest())
	require.ErrorIs(t, err, ErrHandoffUnavailable)

	f = newHandoffFixture()
	f.service.Links = nil
	_, err = f.service.ExchangeHandoff(context.Background(), strings.Repeat("a", 43), "")
	require.ErrorIs(t, err, ErrHandoffUnavailable)
}

func TestCodesComeFromTheRandomSource(t *testing.T) {
	f := newHandoffFixture()
	f.service.Random = bytes.NewReader(make([]byte, 64))
	first, err := f.service.MintHandoff(context.Background(), mintRequest())
	require.NoError(t, err)
	require.Equal(t, strings.Repeat("A", 43), codeFromURL(t, first.URL, "link"))
	// The same code again is refused rather than overwriting the first.
	_, err = f.service.MintHandoff(context.Background(), mintRequest())
	require.ErrorIs(t, err, ErrHandoffUnavailable)
}

func TestLinkRequiresTheSameVerifiedEmail(t *testing.T) {
	f := newHandoffFixture()
	f.users["t1/u2"] = userServiceTypes.GetTenantUser{ID: "u2", Email: "victim@example.org", IsEmailVerified: true}
	result, _ := f.service.MintHandoff(context.Background(), mintRequest())

	// A signed-in victim opening someone else's link code must not be linked.
	_, err := f.service.CompleteHandoffLink(context.Background(), codeFromURL(t, result.URL, "link"), "t1", "u2")

	require.ErrorIs(t, err, ErrHandoffNotAllowed)
	require.Empty(t, f.links.saved)
}
