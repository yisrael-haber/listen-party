package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	pbauth "github.com/pocketbase/pocketbase/tools/auth"
	"github.com/pocketbase/pocketbase/tools/hook"
	"github.com/pocketbase/pocketbase/ui"
)

type Role string

const (
	RoleAdmin Role = "admin"

	defaultAdminEmail    = "admin@listen-party.local"
	defaultAdminPassword = "admin"
	sessionCookieName    = "listen_party_auth"
	sessionKeyCookieName = "listen_party_session"
	sessionDuration      = 24 * time.Hour
	usersCollection      = "users"
)

type UserInfo struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	DisplayName string   `json:"display_name,omitempty"`
	Role        Role     `json:"role,omitempty"`
	Groups      []string `json:"groups"`
	SessionKey  string   `json:"-"`
}

func (u UserInfo) Display() string {
	if name := strings.TrimSpace(u.DisplayName); name != "" {
		return name
	}
	return strings.TrimSpace(u.Username)
}

var loginTemplate = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>listen-party sign in</title>
<style>
html,body{height:100%;margin:0;background:#111;color:#eee;font:16px system-ui,sans-serif}
body{display:grid;place-items:center}
main{width:min(380px,calc(100vw - 32px))}
h1{font-size:24px;margin:0 0 24px}
label{display:block;margin:14px 0 6px;color:#bbb}
input{box-sizing:border-box;width:100%;padding:11px 12px;border:1px solid #444;border-radius:6px;background:#1d1d1d;color:#fff}
button{width:100%;margin-top:20px;padding:11px 12px;border:0;border-radius:6px;background:#2f9e75;color:#fff;font-weight:700;cursor:pointer}
button.secondary{background:#25272b}
.error{margin:0 0 16px;padding:10px 12px;border-radius:6px;background:#772a33;color:#ffdce1}
a{color:#7cc7ff}
</style>
</head>
<body>
<main>
<h1>Sign in</h1>
{{if .Error}}<p class="error">{{.Error}}</p>{{end}}
<form method="post" action="/login">
<input type="hidden" name="return" value="{{.Return}}">
<label for="identity">Username</label>
<input id="identity" name="identity" type="text" autocomplete="username" required autofocus>
<label for="password">Password</label>
<input id="password" name="password" type="password" autocomplete="current-password" required>
<button type="submit">Sign in</button>
</form>
{{if .KeycloakEnabled}}
<button id="keycloakLogin" class="secondary" type="button">Sign in with Keycloak</button>
{{end}}
</main>
{{if .KeycloakEnabled}}
<script>
const oauthStoreKey = "listen-party-oauth";
const keycloakButton = document.getElementById("keycloakLogin");
if (keycloakButton) {
  keycloakButton.addEventListener("click", async () => {
    const res = await fetch("/api/collections/users/auth-methods");
    if (!res.ok) throw new Error(await res.text());
    const methods = await res.json();
    const provider = (methods.oauth2?.providers || []).find((p) => p.name === "oidc");
    if (!provider) throw new Error("Keycloak provider is not configured");
    const redirectURL = location.origin + "/login/oauth/callback";
    sessionStorage.setItem(oauthStoreKey, JSON.stringify({
      state: provider.state,
      codeVerifier: provider.codeVerifier,
      redirectURL,
      returnTo: new URLSearchParams(location.search).get("return") || "/",
    }));
    location.href = provider.authURL + encodeURIComponent(redirectURL) + "&prompt=login";
  });
}
if (location.pathname === "/login/oauth/callback") {
  (async () => {
    const params = new URLSearchParams(location.search);
    const stored = JSON.parse(sessionStorage.getItem(oauthStoreKey) || "{}");
    sessionStorage.removeItem(oauthStoreKey);
    if (!stored.state || stored.state !== params.get("state")) {
      throw new Error("Invalid OAuth state");
    }
    const res = await fetch("/api/collections/users/auth-with-oauth2", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({
        provider: "oidc",
        code: params.get("code"),
        codeVerifier: stored.codeVerifier || "",
        redirectURL: stored.redirectURL,
      }),
    });
    if (!res.ok) throw new Error(await res.text());
    location.href = stored.returnTo || "/";
  })().catch((err) => {
    document.querySelector(".error")?.remove();
    const error = document.createElement("p");
    error.className = "error";
    error.textContent = err.message || "Keycloak login failed";
    document.querySelector("main").prepend(error);
  });
}
</script>
{{end}}
</body>
</html>`))

type Config struct {
	DataDir             string     `json:"-"`
	BootstrapAdminEmail string     `json:"-"`
	Keycloak            OIDCConfig `json:"keycloak"`
}

type OIDCConfig struct {
	Enabled      bool   `json:"enabled"`
	IssuerURL    string `json:"issuer_url"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	DisplayName  string `json:"display_name"`
}

type Service struct {
	app     *pocketbase.PocketBase
	handler http.Handler
}

func Open(cfg Config) (*Service, error) {
	if cfg.DataDir == "" {
		return nil, errors.New("auth.data_dir is required")
	}
	if cfg.BootstrapAdminEmail == "" {
		cfg.BootstrapAdminEmail = defaultAdminEmail
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, err
	}

	app := pocketbase.NewWithConfig(pocketbase.Config{
		DefaultDataDir:  cfg.DataDir,
		HideStartBanner: true,
	})
	if err := app.Bootstrap(); err != nil {
		return nil, err
	}
	if err := app.RunAllMigrations(); err != nil {
		return nil, err
	}
	if err := ensureUserMetadata(app); err != nil {
		return nil, err
	}
	if err := configureOIDC(app, cfg.Keycloak); err != nil {
		return nil, err
	}
	if err := allowBootstrapAdminPassword(app); err != nil {
		return nil, err
	}
	if err := ensureBootstrapAdmin(app, cfg.BootstrapAdminEmail); err != nil {
		return nil, err
	}
	bindSessionCookie(app)
	bindOAuthDefaults(app)

	handler, err := buildHandler(app, cfg)
	if err != nil {
		return nil, err
	}
	return &Service{app: app, handler: handler}, nil
}

func (s *Service) Close() error {
	if s == nil || s.app == nil {
		return nil
	}
	return s.app.ResetBootstrapState()
}

func (s *Service) Handler() http.Handler {
	return s.handler
}

func (s *Service) Require(roles ...Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !s.Authorized(r, roles...) {
				if wantsHTML(r) {
					http.Redirect(w, r, "/login?return="+url.QueryEscape(loginReturn(r)), http.StatusFound)
					return
				}
				http.Error(w, "authentication required", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (s *Service) Authorized(r *http.Request, roles ...Role) bool {
	user, ok := s.CurrentUser(r)
	if !ok {
		return false
	}
	if len(roles) == 0 {
		return true
	}
	for _, role := range roles {
		if role == RoleAdmin && user.Role == RoleAdmin {
			return true
		}
	}
	return false
}

func (s *Service) CurrentUser(r *http.Request) (UserInfo, bool) {
	if s == nil || s.app == nil {
		return UserInfo{}, false
	}
	token := authToken(r)
	if token == "" {
		return UserInfo{}, false
	}
	record, err := s.app.FindAuthRecordByToken(token, core.TokenTypeAuth)
	if err != nil || record == nil {
		return UserInfo{}, false
	}
	if record.Collection().Name != usersCollection || !record.GetBool("enabled") {
		return UserInfo{}, false
	}
	var role Role
	if record.GetString("app_role") == string(RoleAdmin) {
		role = RoleAdmin
	}
	return UserInfo{
		ID:          record.Id,
		Username:    record.GetString("username"),
		DisplayName: record.GetString("name"),
		Role:        role,
		Groups:      effectiveGroups(record.GetString("groups"), record.GetString("sso_groups")),
		SessionKey:  requestSessionKey(r, token),
	}, true
}

func bindSessionCookie(app core.App) {
	app.OnRecordAuthRequest().Bind(&hook.Handler[*core.RecordAuthRequestEvent]{
		Id: "listenPartySessionCookie",
		Func: func(e *core.RecordAuthRequestEvent) error {
			if e.Token != "" {
				if err := setSessionCookie(e.Response, e.Request, e.Token); err != nil {
					return err
				}
			}
			return e.Next()
		},
	})
}

func DataDir(configDir string) string {
	return filepath.Join(configDir, "auth")
}

func DefaultBootstrapAdminEmail() string {
	return defaultAdminEmail
}

func DefaultConfig(configDir string) Config {
	return Config{
		DataDir:             DataDir(configDir),
		BootstrapAdminEmail: defaultAdminEmail,
	}
}

func configureOIDC(app core.App, cfg OIDCConfig) error {
	if !cfg.Enabled {
		return nil
	}
	if cfg.IssuerURL == "" {
		return errors.New("auth.pocketbase.keycloak.issuer_url is required when keycloak is enabled")
	}
	if cfg.ClientID == "" {
		return errors.New("auth.pocketbase.keycloak.client_id is required when keycloak is enabled")
	}
	if cfg.ClientSecret == "" {
		return errors.New("auth.pocketbase.keycloak.client_secret is required when keycloak is enabled")
	}
	if cfg.DisplayName == "" {
		cfg.DisplayName = "Keycloak"
	}
	issuer := strings.TrimRight(cfg.IssuerURL, "/")
	if _, err := url.ParseRequestURI(issuer); err != nil {
		return err
	}

	collection, err := app.FindCollectionByNameOrId(usersCollection)
	if err != nil {
		return err
	}
	collection.OAuth2.Enabled = true
	collection.OAuth2.MappedFields.Name = "name"
	collection.OAuth2.MappedFields.Username = "username"
	collection.CreateRule = stringPtr(`@request.context = "oauth2"`)

	provider := core.OAuth2ProviderConfig{
		Name:         "oidc",
		DisplayName:  cfg.DisplayName,
		ClientId:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		AuthURL:      issuer + "/protocol/openid-connect/auth",
		TokenURL:     issuer + "/protocol/openid-connect/token",
		UserInfoURL:  issuer + "/protocol/openid-connect/userinfo",
		Extra: map[string]any{
			"issuers": []string{issuer},
		},
	}
	upsertOAuthProvider(collection, provider)
	return app.Save(collection)
}

func upsertOAuthProvider(collection *core.Collection, provider core.OAuth2ProviderConfig) {
	for i, existing := range collection.OAuth2.Providers {
		if existing.Name == provider.Name {
			collection.OAuth2.Providers[i] = provider
			return
		}
	}
	collection.OAuth2.Providers = append(collection.OAuth2.Providers, provider)
}

func bindOAuthDefaults(app core.App) {
	app.OnRecordAuthWithOAuth2Request(usersCollection).Bind(&hook.Handler[*core.RecordAuthWithOAuth2RequestEvent]{
		Id: "listenPartyOAuthDefaults",
		Func: func(e *core.RecordAuthWithOAuth2RequestEvent) error {
			if e.Collection.Name != usersCollection {
				return e.Next()
			}
			groups, hasGroups := oauthGroups(e.OAuth2User)
			groupValue := strings.Join(groups, ", ")
			if e.CreateData == nil {
				e.CreateData = map[string]any{}
			}
			if _, ok := e.CreateData["enabled"]; !ok {
				e.CreateData["enabled"] = true
			}
			if e.OAuth2User != nil && e.OAuth2User.Username != "" {
				if _, ok := e.CreateData["username"]; !ok {
					e.CreateData["username"] = e.OAuth2User.Username
				}
			}
			if hasGroups {
				e.CreateData["sso_groups"] = groupValue
			}
			if err := e.Next(); err != nil {
				return err
			}
			if hasGroups && e.Record != nil && e.Record.GetString("sso_groups") != groupValue {
				e.Record.Set("sso_groups", groupValue)
				return e.App.Save(e.Record)
			}
			return nil
		},
	})
}

func stringPtr(value string) *string {
	return &value
}

func ensureBootstrapAdmin(app core.App, email string) error {
	if _, err := app.FindAuthRecordByEmail(core.CollectionNameSuperusers, email); err == nil {
		return nil
	}
	collection, err := app.FindCachedCollectionByNameOrId(core.CollectionNameSuperusers)
	if err != nil {
		return err
	}
	record := core.NewRecord(collection)
	record.SetEmail(email)
	record.SetPassword(defaultAdminPassword)
	return app.Save(record)
}

func allowBootstrapAdminPassword(app core.App) error {
	collection, err := app.FindCollectionByNameOrId(core.CollectionNameSuperusers)
	if err != nil {
		return err
	}
	field, ok := collection.Fields.GetByName("password").(*core.PasswordField)
	if !ok || field.Min <= 0 || field.Min <= len(defaultAdminPassword) {
		return nil
	}
	field.Min = len(defaultAdminPassword)
	return app.Save(collection)
}

func ensureUserMetadata(app core.App) error {
	collection, err := app.FindCollectionByNameOrId(usersCollection)
	if err != nil {
		return err
	}
	if err := purgeLegacyUsers(app, collection); err != nil {
		return err
	}
	changed := false
	if emailField, ok := collection.Fields.GetByName(core.FieldNameEmail).(*core.EmailField); ok && emailField.Required {
		emailField.Required = false
		changed = true
	}
	if collection.Fields.GetByName("username") == nil {
		collection.Fields.Add(&core.TextField{
			Name:        "username",
			Help:        "Username used to sign in to listen-party.",
			Min:         1,
			Max:         80,
			Presentable: true,
		})
		changed = true
	}
	if collection.Fields.GetByName("enabled") == nil {
		collection.Fields.Add(&core.BoolField{
			Name: "enabled",
			Help: "Allows this user to access listen-party.",
		})
		changed = true
	}
	if collection.Fields.GetByName("app_role") == nil {
		collection.Fields.Add(&core.TextField{
			Name: "app_role",
			Help: "Set to admin for unrestricted listen-party access; otherwise leave empty.",
			Max:  32,
		})
		changed = true
	}
	if collection.Fields.GetByName("room_ids") != nil {
		collection.Fields.RemoveByName("room_ids")
		changed = true
	}
	if collection.Fields.GetByName("groups") == nil {
		collection.Fields.Add(&core.TextField{
			Name: "groups",
			Help: "Comma-separated application-managed groups used for listen-party room permissions.",
			Max:  1000,
		})
		changed = true
	}
	if collection.Fields.GetByName("local_groups") != nil {
		collection.Fields.RemoveByName("local_groups")
		changed = true
	}
	if collection.Fields.GetByName("sso_groups") == nil {
		collection.Fields.Add(&core.TextField{
			Name: "sso_groups",
			Help: "Comma-separated groups synchronized from the SSO provider on login.",
			Max:  1000,
		})
		changed = true
	}
	if !collection.PasswordAuth.Enabled || !slices.Equal(collection.PasswordAuth.IdentityFields, []string{"username"}) {
		collection.PasswordAuth.Enabled = true
		collection.PasswordAuth.IdentityFields = []string{"username"}
		changed = true
	}
	if collection.GetIndex("idx_users_username") == "" {
		collection.AddIndex("idx_users_username", true, "`username`", "")
		changed = true
	}
	if collection.AuthToken.Duration != int64(sessionDuration/time.Second) {
		collection.AuthToken.Duration = int64(sessionDuration / time.Second)
		changed = true
	}
	if changed {
		if err := app.Save(collection); err != nil {
			return err
		}
	}
	return syncMissingUserColumns(app, collection)
}

func purgeLegacyUsers(app core.App, collection *core.Collection) error {
	if collection.Fields.GetByName("username") != nil &&
		collection.PasswordAuth.Enabled &&
		slices.Equal(collection.PasswordAuth.IdentityFields, []string{"username"}) &&
		collection.GetIndex("idx_users_username") != "" {
		return nil
	}
	records, err := app.FindAllRecords(collection)
	if err != nil {
		return err
	}
	for _, record := range records {
		if err := app.Delete(record); err != nil {
			return err
		}
	}
	return nil
}

func syncMissingUserColumns(app core.App, collection *core.Collection) error {
	columns, err := app.TableColumns(collection.Name)
	if err != nil {
		return err
	}
	for _, fieldName := range []string{"username", "enabled", "app_role", "groups", "sso_groups"} {
		if slices.Contains(columns, fieldName) {
			continue
		}
		field := collection.Fields.GetByName(fieldName)
		if field == nil {
			continue
		}
		if _, err := app.DB().AddColumn(collection.Name, fieldName, field.ColumnType(app)).Execute(); err != nil {
			return err
		}
	}
	return nil
}

func splitMetadataList(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\t' || r == ' '
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" && !slices.Contains(out, part) {
			out = append(out, part)
		}
	}
	return out
}

func effectiveGroups(groupsValue, sso string) []string {
	groups := splitMetadataList(groupsValue)
	for _, group := range splitMetadataList(sso) {
		if !slices.Contains(groups, group) {
			groups = append(groups, group)
		}
	}
	return groups
}

func oauthGroups(user *pbauth.AuthUser) ([]string, bool) {
	if user == nil || user.RawUser == nil {
		return nil, false
	}
	raw, ok := user.RawUser["groups"]
	if !ok {
		return nil, false
	}
	return normalizeOAuthGroups(raw), true
}

func normalizeOAuthGroups(raw any) []string {
	var values []string
	switch groups := raw.(type) {
	case []string:
		values = groups
	case []any:
		for _, item := range groups {
			if value, ok := item.(string); ok {
				values = append(values, value)
			}
		}
	case string:
		values = strings.FieldsFunc(groups, func(r rune) bool {
			return r == ',' || r == '\n' || r == '\t'
		})
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		value = strings.TrimPrefix(value, "/")
		if value != "" && !slices.Contains(out, value) {
			out = append(out, value)
		}
	}
	return out
}

func buildHandler(app core.App, cfg Config) (http.Handler, error) {
	router, err := apis.NewRouter(app)
	if err != nil {
		return nil, err
	}
	if ui.DistDirFS != nil {
		router.GET("/_/{path...}", apis.Static(ui.DistDirFS, false)).
			Bind(apis.Gzip())
	}
	mux, err := router.BuildMux()
	if err != nil {
		return nil, err
	}
	root := http.NewServeMux()
	root.Handle("/", mux)
	root.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		writeLogin(w, r, cfg, "")
	})
	root.HandleFunc("GET /login/oauth/callback", func(w http.ResponseWriter, r *http.Request) {
		writeLogin(w, r, cfg, "")
	})
	root.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
		handleLogin(app, cfg, w, r)
	})
	root.HandleFunc("/logout", func(w http.ResponseWriter, r *http.Request) {
		clearSessionCookie(w, r)
		http.Redirect(w, r, "/login", http.StatusFound)
	})
	root.HandleFunc("/authAdmin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/_/", http.StatusFound)
	})
	root.HandleFunc("/authAdmin/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/_/", http.StatusFound)
	})
	return root, nil
}

func handleLogin(app core.App, cfg Config, w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	identity := strings.TrimSpace(r.Form.Get("identity"))
	password := r.Form.Get("password")
	returnTo := cleanReturnPath(r.Form.Get("return"))
	record, err := authenticate(app, identity, password)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		writeLogin(w, r, cfg, err.Error())
		return
	}
	token, err := record.NewAuthToken()
	if err != nil {
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}
	if err := setSessionCookie(w, r, token); err != nil {
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, returnTo, http.StatusFound)
}

func authenticate(app core.App, identity, password string) (*core.Record, error) {
	record, err := app.FindFirstRecordByData(usersCollection, "username", identity)
	if err != nil || !record.ValidatePassword(password) {
		return nil, errors.New("invalid username or password")
	}
	if !record.GetBool("enabled") {
		return nil, errors.New("user is disabled")
	}
	return record, nil
}

func writeLogin(w http.ResponseWriter, r *http.Request, cfg Config, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := struct {
		Error           string
		Return          string
		KeycloakEnabled bool
	}{
		Error:           message,
		Return:          cleanReturnPath(r.URL.Query().Get("return")),
		KeycloakEnabled: cfg.Keycloak.Enabled,
	}
	if data.Return == "/" && r.Method == http.MethodPost {
		data.Return = cleanReturnPath(r.Form.Get("return"))
	}
	if err := loginTemplate.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string) error {
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		return err
	}
	expires := time.Now().Add(sessionDuration)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
		Expires:  expires,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     sessionKeyCookieName,
		Value:    base64.RawURLEncoding.EncodeToString(keyBytes),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
		Expires:  expires,
	})
	return nil
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	for _, name := range []string{sessionCookieName, sessionKeyCookieName} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Secure:   r.TLS != nil,
			MaxAge:   -1,
			Expires:  time.Unix(0, 0),
		})
	}
}

func requestSessionKey(r *http.Request, token string) string {
	if bearerToken(r.Header.Get("Authorization")) == "" {
		if cookie, err := r.Cookie(sessionKeyCookieName); err == nil && strings.TrimSpace(cookie.Value) != "" {
			return "session:" + strings.TrimSpace(cookie.Value)
		}
	}
	sum := sha256.Sum256([]byte(token))
	return "token:" + base64.RawURLEncoding.EncodeToString(sum[:])
}

func bearerToken(header string) string {
	header = strings.TrimSpace(header)
	if len(header) > 7 && strings.EqualFold(header[:7], "Bearer ") {
		return strings.TrimSpace(header[7:])
	}
	return header
}

func authToken(r *http.Request) string {
	if token := bearerToken(r.Header.Get("Authorization")); token != "" {
		return token
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(cookie.Value)
}

func loginReturn(r *http.Request) string {
	return cleanReturnPath(r.URL.RequestURI())
}

func cleanReturnPath(raw string) string {
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return "/"
	}
	if raw == "/login" || strings.HasPrefix(raw, "/login/") ||
		raw == "/authAdmin" || strings.HasPrefix(raw, "/authAdmin/") ||
		strings.HasPrefix(raw, "/_/") {
		return "/"
	}
	return raw
}

func wantsHTML(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	accept := r.Header.Get("Accept")
	return accept == "" || strings.Contains(accept, "text/html")
}
