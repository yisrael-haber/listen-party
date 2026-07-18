package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	pbauth "github.com/pocketbase/pocketbase/tools/auth"
)

func TestOpenBootstrapsAdminAndAuthAdminRedirect(t *testing.T) {
	svc, err := Open(Config{
		DataDir:             filepath.Join(t.TempDir(), "auth"),
		BootstrapAdminEmail: "admin@listen-party.local",
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer svc.Close()

	admin, err := svc.app.FindAuthRecordByEmail(core.CollectionNameSuperusers, "admin@listen-party.local")
	if err != nil {
		t.Fatalf("bootstrap admin missing: %v", err)
	}
	if !admin.ValidatePassword(defaultAdminPassword) {
		t.Fatal("bootstrap admin password was not initialized")
	}

	req := httptest.NewRequest(http.MethodGet, "/authAdmin", nil)
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("/authAdmin status = %d, want %d", rec.Code, http.StatusFound)
	}
	if got := rec.Header().Get("Location"); got != "/_/" {
		t.Fatalf("/authAdmin location = %q, want /_/", got)
	}

	users, err := svc.app.FindCachedCollectionByNameOrId(usersCollection)
	if err != nil {
		t.Fatal(err)
	}
	if users.Fields.GetByName("enabled") == nil {
		t.Fatal("users collection missing enabled field")
	}
	if users.Fields.GetByName("username") == nil {
		t.Fatal("users collection missing username field")
	}
	if users.Fields.GetByName("app_role") == nil {
		t.Fatal("users collection missing app_role field")
	}
	if users.Fields.GetByName("groups") == nil {
		t.Fatal("users collection missing groups field")
	}
	if users.Fields.GetByName("sso_groups") == nil {
		t.Fatal("users collection missing sso_groups field")
	}
	if users.Fields.GetByName("local_groups") != nil {
		t.Fatal("users collection still has local_groups field")
	}
	if got := users.PasswordAuth.IdentityFields; len(got) != 1 || got[0] != "username" {
		t.Fatalf("users identity fields = %v, want [username]", got)
	}
	if users.GetIndex("idx_users_username") == "" {
		t.Fatal("users collection missing username unique index")
	}
	if users.AuthToken.Duration != int64(sessionDuration/time.Second) {
		t.Fatalf("auth token duration = %d, want %d", users.AuthToken.Duration, int64(sessionDuration/time.Second))
	}
	columns, err := svc.app.TableColumns(usersCollection)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(columns, "username") {
		t.Fatal("users table missing username column")
	}
	if !contains(columns, "enabled") {
		t.Fatal("users table missing enabled column")
	}
	if !contains(columns, "app_role") {
		t.Fatal("users table missing app_role column")
	}
}

func TestBootstrapSuperuserCanUseAdminAPI(t *testing.T) {
	svc, err := Open(Config{
		DataDir:             filepath.Join(t.TempDir(), "auth"),
		BootstrapAdminEmail: "admin@listen-party.local",
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer svc.Close()

	body := `{"identity":"admin@listen-party.local","password":"admin"}`
	req := httptest.NewRequest(http.MethodPost, "/api/collections/_superusers/auth-with-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("superuser login status = %d: %s", rec.Code, rec.Body.String())
	}
	var authResponse struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &authResponse); err != nil {
		t.Fatal(err)
	}
	if authResponse.Token == "" {
		t.Fatal("superuser login returned no token")
	}

	req = httptest.NewRequest(http.MethodGet, "/api/collections", nil)
	req.Header.Set("Authorization", "Bearer "+authResponse.Token)
	rec = httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("authenticated admin API status = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAuthorizedRejectsSuperuserTokens(t *testing.T) {
	svc, err := Open(Config{
		DataDir:             filepath.Join(t.TempDir(), "auth"),
		BootstrapAdminEmail: "admin@listen-party.local",
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer svc.Close()

	admin, err := svc.app.FindAuthRecordByEmail(core.CollectionNameSuperusers, "admin@listen-party.local")
	if err != nil {
		t.Fatal(err)
	}
	token, err := admin.NewAuthToken()
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if svc.Authorized(req, RoleAdmin) {
		t.Fatal("superuser token was authorized for app access")
	}
}

func TestAuthorizedAcceptsSessionCookie(t *testing.T) {
	svc, err := Open(Config{
		DataDir:             filepath.Join(t.TempDir(), "auth"),
		BootstrapAdminEmail: "admin@listen-party.local",
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer svc.Close()

	admin, err := createAppUser(svc, "admin", "changed-password", RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	token, err := admin.NewAuthToken()
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	req.AddCookie(&http.Cookie{Name: sessionKeyCookieName, Value: "browser-session"})
	if !svc.Authorized(req, RoleAdmin) {
		t.Fatal("admin session cookie rejected")
	}
	user, ok := svc.CurrentUser(req)
	if !ok || user.SessionKey != "session:browser-session" {
		t.Fatalf("session key = %q, authorized = %v", user.SessionKey, ok)
	}
}

func TestAuthorizedAcceptsRegularUserWithoutRole(t *testing.T) {
	svc, err := Open(Config{
		DataDir:             filepath.Join(t.TempDir(), "auth"),
		BootstrapAdminEmail: "admin@listen-party.local",
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer svc.Close()

	user, err := createAppUser(svc, "alice", "changed-password", "")
	if err != nil {
		t.Fatal(err)
	}
	token, err := user.NewAuthToken()
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	if !svc.Authorized(req) {
		t.Fatal("regular user was not authorized as an enabled user")
	}
	if svc.Authorized(req, RoleAdmin) {
		t.Fatal("regular user was authorized as admin")
	}
}

func TestRequireRedirectsHTMLRequests(t *testing.T) {
	svc, err := Open(Config{
		DataDir:             filepath.Join(t.TempDir(), "auth"),
		BootstrapAdminEmail: "admin@listen-party.local",
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer svc.Close()

	handler := svc.Require()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
	}
	if got := rec.Header().Get("Location"); got != "/login?return=%2F" {
		t.Fatalf("location = %q, want /login?return=%%2F", got)
	}
}

func TestLoginAuthenticatesEnabledUser(t *testing.T) {
	svc, err := Open(Config{
		DataDir:             filepath.Join(t.TempDir(), "auth"),
		BootstrapAdminEmail: "admin@listen-party.local",
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer svc.Close()

	users, err := svc.app.FindCollectionByNameOrId(usersCollection)
	if err != nil {
		t.Fatal(err)
	}
	record := core.NewRecord(users)
	record.Set("username", "first_user")
	record.SetPassword("user-password")
	record.Set("enabled", true)
	if err := svc.app.Save(record); err != nil {
		t.Fatal(err)
	}

	form := url.Values{}
	form.Set("identity", "first_user")
	form.Set("password", "user-password")
	form.Set("return", "/")
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
	}
	if got := rec.Header().Get("Location"); got != "/" {
		t.Fatalf("location = %q, want /", got)
	}
	cookieNames := map[string]bool{}
	for _, cookie := range rec.Result().Cookies() {
		cookieNames[cookie.Name] = true
	}
	if !cookieNames[sessionCookieName] || !cookieNames[sessionKeyCookieName] {
		t.Fatalf("login cookies = %#v", cookieNames)
	}
}

func TestLoginRejectsEmailIdentity(t *testing.T) {
	svc, err := Open(Config{
		DataDir:             filepath.Join(t.TempDir(), "auth"),
		BootstrapAdminEmail: "admin@listen-party.local",
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer svc.Close()

	users, err := svc.app.FindCollectionByNameOrId(usersCollection)
	if err != nil {
		t.Fatal(err)
	}
	record := core.NewRecord(users)
	record.Set("username", "second_user")
	record.SetEmail("second@example.com")
	record.SetPassword("user-password")
	record.Set("enabled", true)
	if err := svc.app.Save(record); err != nil {
		t.Fatal(err)
	}

	form := url.Values{}
	form.Set("identity", "second@example.com")
	form.Set("password", "user-password")
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestLogoutClearsAuthenticationAndSessionCookies(t *testing.T) {
	svc, err := Open(Config{
		DataDir:             filepath.Join(t.TempDir(), "auth"),
		BootstrapAdminEmail: "admin@listen-party.local",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	req := httptest.NewRequest(http.MethodGet, "/logout", nil)
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	cleared := map[string]bool{}
	for _, cookie := range rec.Result().Cookies() {
		if cookie.MaxAge < 0 {
			cleared[cookie.Name] = true
		}
	}
	if !cleared[sessionCookieName] || !cleared[sessionKeyCookieName] {
		t.Fatalf("cleared cookies = %#v", cleared)
	}
}

func TestSessionCookieKeysAreUniquePerLogin(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	keys := make([]string, 0, 2)
	for range 2 {
		rec := httptest.NewRecorder()
		if err := setSessionCookie(rec, req, "same-auth-token"); err != nil {
			t.Fatal(err)
		}
		for _, cookie := range rec.Result().Cookies() {
			if cookie.Name == sessionKeyCookieName {
				keys = append(keys, cookie.Value)
			}
		}
	}
	if len(keys) != 2 || keys[0] == keys[1] {
		t.Fatalf("session keys = %#v, want two unique values", keys)
	}
}

func TestOpenConfiguresKeycloakOIDC(t *testing.T) {
	svc, err := Open(Config{
		DataDir:             filepath.Join(t.TempDir(), "auth"),
		BootstrapAdminEmail: "admin@listen-party.local",
		Keycloak: OIDCConfig{
			Enabled:      true,
			IssuerURL:    "http://127.0.0.1:10000/realms/listen-party",
			ClientID:     "listen-party",
			ClientSecret: "secret",
		},
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer svc.Close()

	users, err := svc.app.FindCollectionByNameOrId(usersCollection)
	if err != nil {
		t.Fatal(err)
	}
	if !users.OAuth2.Enabled {
		t.Fatal("oauth2 was not enabled")
	}
	provider, ok := users.OAuth2.GetProviderConfig("oidc")
	if !ok {
		t.Fatal("oidc provider missing")
	}
	if provider.DisplayName != "Keycloak" {
		t.Fatalf("display name = %q, want Keycloak", provider.DisplayName)
	}
	if provider.AuthURL != "http://127.0.0.1:10000/realms/listen-party/protocol/openid-connect/auth" {
		t.Fatalf("auth url = %q", provider.AuthURL)
	}
	if users.OAuth2.MappedFields.Username != "username" {
		t.Fatalf("mapped username = %q, want username", users.OAuth2.MappedFields.Username)
	}
	if users.CreateRule == nil || *users.CreateRule != `@request.context = "oauth2"` {
		t.Fatalf("create rule = %v, want oauth2-only", users.CreateRule)
	}

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "Sign in with Keycloak") {
		t.Fatal("login page missing Keycloak button")
	}
}

func TestBearerToken(t *testing.T) {
	if got := bearerToken("Bearer abc"); got != "abc" {
		t.Fatalf("bearerToken() = %q, want abc", got)
	}
	if got := bearerToken("abc"); got != "abc" {
		t.Fatalf("bearerToken(raw) = %q, want abc", got)
	}
}

func TestOAuthGroupsReadsKeycloakGroupsClaim(t *testing.T) {
	groups, ok := oauthGroups(&pbauth.AuthUser{RawUser: map[string]any{
		"groups": []any{"/math", "staff", "/math"},
	}})
	if !ok {
		t.Fatal("groups claim was not detected")
	}
	if strings.Join(groups, ",") != "math,staff" {
		t.Fatalf("groups = %v, want [math staff]", groups)
	}
}

func TestOAuthGroupsLeavesMissingClaimAlone(t *testing.T) {
	groups, ok := oauthGroups(&pbauth.AuthUser{RawUser: map[string]any{
		"preferred_username": "alice",
	}})
	if ok {
		t.Fatalf("groups claim detected unexpectedly: %v", groups)
	}
}

func TestEffectiveGroupsUnionsLocalAndSSOGroups(t *testing.T) {
	got := effectiveGroups("staff, room-admins", "staff, /engineering")
	if want := "staff,room-admins,/engineering"; strings.Join(got, ",") != want {
		t.Fatalf("effectiveGroups() = %v, want %s", got, want)
	}
}

func TestCurrentUserUsesLocalAndSSOGroups(t *testing.T) {
	svc, err := Open(Config{
		DataDir:             filepath.Join(t.TempDir(), "auth"),
		BootstrapAdminEmail: "admin@listen-party.local",
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer svc.Close()

	record, err := createAppUser(svc, "alice", "changed-password", "")
	if err != nil {
		t.Fatal(err)
	}
	record.Set("groups", "staff, room-admins")
	record.Set("sso_groups", "staff, engineering")
	if err := svc.app.Save(record); err != nil {
		t.Fatal(err)
	}
	token, err := record.NewAuthToken()
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	user, ok := svc.CurrentUser(req)
	if !ok {
		t.Fatal("CurrentUser() rejected valid user")
	}
	if want := "staff,room-admins,engineering"; strings.Join(user.Groups, ",") != want {
		t.Fatalf("groups = %v, want %s", user.Groups, want)
	}
}

func TestEnsureUserMetadataRepairsMissingColumns(t *testing.T) {
	svc, err := Open(Config{
		DataDir:             filepath.Join(t.TempDir(), "auth"),
		BootstrapAdminEmail: "admin@listen-party.local",
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer svc.Close()

	if _, err := svc.app.DB().DropColumn(usersCollection, "groups").Execute(); err != nil {
		t.Fatalf("drop groups column: %v", err)
	}
	if err := ensureUserMetadata(svc.app); err != nil {
		t.Fatalf("ensureUserMetadata() error = %v", err)
	}
	columns, err := svc.app.TableColumns(usersCollection)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(columns, "groups") {
		t.Fatal("groups column was not repaired")
	}
}

func TestListEnabledUsersReturnsOnlySafeFields(t *testing.T) {
	svc, err := Open(Config{
		DataDir:             filepath.Join(t.TempDir(), "auth"),
		BootstrapAdminEmail: "admin@listen-party.local",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	bob, err := createAppUser(svc, "bob", "changed-password", "")
	if err != nil {
		t.Fatal(err)
	}
	bob.Set("name", "Bob Example")
	if err := svc.app.Save(bob); err != nil {
		t.Fatal(err)
	}
	disabled, err := createAppUser(svc, "disabled", "changed-password", "")
	if err != nil {
		t.Fatal(err)
	}
	disabled.Set("enabled", false)
	if err := svc.app.Save(disabled); err != nil {
		t.Fatal(err)
	}

	users, err := svc.ListEnabledUsers()
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].ID != bob.Id || users[0].Username != "bob" || users[0].DisplayName != "Bob Example" {
		t.Fatalf("users = %#v", users)
	}
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func createAppUser(svc *Service, username, password string, role Role) (*core.Record, error) {
	users, err := svc.app.FindCollectionByNameOrId(usersCollection)
	if err != nil {
		return nil, err
	}
	record := core.NewRecord(users)
	record.Set("username", username)
	record.SetPassword(password)
	record.Set("enabled", true)
	record.Set("app_role", string(role))
	if err := svc.app.Save(record); err != nil {
		return nil, err
	}
	return record, nil
}
