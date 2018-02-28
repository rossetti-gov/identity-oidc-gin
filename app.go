package main

import (
  "encoding/base64"
  "encoding/json"
  "fmt"
  "io/ioutil"
  "math/rand"
  "net/http"
  "net/url"
  "os"
  "reflect"
  "time"
  "github.com/gin-gonic/gin"
  "github.com/markbates/goth"
  "github.com/markbates/goth/providers/openidConnect"
  "github.com/s2t2/goth/gothic" // exports session access methods; TODO: switch back to markbates/gothic after merge of https://github.com/markbates/goth/pull/210
  "github.com/dgrijalva/jwt-go"
  "github.com/joho/godotenv"
)

var providerUrl string = "http://localhost:3000" // refers to the login.gov server
const providerName = "openid-connect" // the OIDC provider name (https://github.com/markbates/goth/blob/master/providers/openidConnect/openidConnect.go#L101)
var clientUrl string = "http://localhost:8080" // refers to this application
var clientId string = "urn:gov:gsa:openidconnect:sp:gin"
var clientSecret string
const userSessionKey = "my_user" // refers to a session key/value pair where user information will be stored

func init()  {
  loadEnvironmentVars()
}

// Configures routes and runs the web server.
func main() {
  configureProvider()

  router := gin.Default()
  router.LoadHTMLGlob("views/*") // load views
  router.Static("/assets", "./assets") // load static assets
  router.GET("/", renderIndex)
  router.GET("/profile", renderProfile)
  router.GET("/auth/login-gov/login/loa-1", login) // TODO: login(1)
  router.GET("/auth/login-gov/login/loa-3", login) // TODO: login(3)
  router.GET("/auth/login-gov/callback", callback)
  router.GET("/auth/login-gov/logout", logout)
  router.GET("/auth/login-gov/logout/rp", logout) // TODO: rpLogout
  router.Run() // listen and serve on 0.0.0.0:8080
}

// Loads environment variables from the .env file, validates them,
// and assigns them to program vars for further reference.
func loadEnvironmentVars()  {
  fmt.Println("------------")
  fmt.Println("ENV")
  fmt.Println("------------")

  err := godotenv.Load()
  if err != nil { fmt.Println("Error loading .env file") }

  if os.Getenv("SESSION_SECRET") == "" { panic("Oh, please set the SESSION_SECRET environment variable!") } // SESSION_SECRET must be set. See: https://github.com/markbates/goth/blob/12866fa2c65b81b6b7defe879dcd4af2477a9a29/gothic/gothic.go#L113

  if os.Getenv("PROVIDER_URL") != "" { providerUrl = os.Getenv("PROVIDER_URL") }
  fmt.Println("PROVIDER_URL:", providerUrl)

  if os.Getenv("CLIENT_URL") != "" { clientUrl = os.Getenv("CLIENT_URL") }
  fmt.Println("CLIENT_URL:", clientUrl)

  if os.Getenv("CLIENT_ID") != "" { clientId = os.Getenv("CLIENT_ID") }
  fmt.Println("CLIENT_ID:", clientId)

  if os.Getenv("CLIENT_SECRET") != "" {
    clientSecret = os.Getenv("CLIENT_SECRET")
    fmt.Println("CLIENT_SECRET: (using a custom value)")
  } else {
    clientSecret = os.Getenv("SESSION_SECRET")
    fmt.Println("CLIENT_SECRET: (using the SESSION_SECRET)")
  }

  fmt.Println("------------")
}

// Registers login.gov as the OIDC identity provider.
// See: https://developers.login.gov/oidc/#configuration.
func configureProvider()  {
  //gothic.GetProviderName = func(req *http.Request) (string, error) { return providerName, nil} // sets the provider's name, bypasses error looking for provider name (although I'm no longer seeing this error). see: https://github.com/markbates/goth/blob/master/gothic/gothic.go#L246

  discoveryUrl := providerUrl + "/.well-known/openid-configuration"
  callbackUrl := clientUrl + "/auth/login-gov/callback"

  provider, err := openidConnect.New(clientId, clientSecret, callbackUrl, discoveryUrl)
  if err != nil { fmt.Println("OIDC PROVIDER ERROR", err) }
  fmt.Println("OIDC PROVIDER:", reflect.TypeOf(provider))
  fmt.Println("------------")

  goth.UseProviders(provider)
}

//
// ROUTE HANDLERS
//

func renderIndex(c *gin.Context) {
  fmt.Println("------------")
  fmt.Println("INDEX")
  fmt.Println("------------")

  c.HTML(http.StatusOK, "index.tmpl", gin.H{"title": "Login.gov OIDC Client (Gin)"})
}

func renderProfile(c *gin.Context) {
  fmt.Println("------------")
  fmt.Println("PROFILE")
  fmt.Println("------------")

  user, err := getUserFromSession(c)
  if err != nil {
    fmt.Println("COULDN'T FIND AN AUTHENTICATED USER", err)
    redirectIndex(c) // TODO: pass a flash message like "Please log in".
  }

  var blocks [5]int
  c.HTML(http.StatusOK, "profile.tmpl", gin.H{"title": "Profile Page", "blocks": blocks, "user": user})
}

func redirectIndex(c *gin.Context){
  c.Redirect(http.StatusTemporaryRedirect, "/")
}

func redirectProfile(c *gin.Context)  {
  c.Redirect(http.StatusTemporaryRedirect, "/profile")
}

// Issues a login.gov authorization request.
// See: https://developers.login.gov/oidc/#authorization.
func login(c *gin.Context)  {
  fmt.Println("------------")
  fmt.Println("LOGIN")
  fmt.Println("------------")

  provider, err := goth.GetProvider(providerName)
  if err != nil { fmt.Println("PROVIDER LOOKUP ERROR") }

  state := generateNonce()

  sesh, err := provider.BeginAuth(state)
  if err != nil { fmt.Println("BEGIN AUTH ERROR") }
  fmt.Println("SESSION:", reflect.TypeOf(sesh), sesh)

  authURL, err := loginGovAuthURL(sesh, state)
  if err != nil { fmt.Println("AUTH URL COMPLIATION ERROR") }

  c.Redirect(http.StatusTemporaryRedirect, authURL)
}

// Handles a login.gov callback, fetches a token, fetches user info, and stores user info in a session.
func callback(c *gin.Context)  {
  fmt.Println("------------")
  fmt.Println("CALLBACK")
  fmt.Println("------------")

  tokenResponse := fetchToken(c)

  gothicUser, err := fetchUserInfo(c, tokenResponse)
  if err != nil { fmt.Println("FETCH USER ERROR", err) }

  js, err := json.Marshal(gothicUser.RawData)
  if err != nil { fmt.Println("JSON MARSHAL ERROR", err) }
  fmt.Println("USER INFO:", string(js))

  err = gothic.StoreInSession(userSessionKey, string(js), c.Request, c.Writer)
  if err != nil { fmt.Println("SESSION STORAGE ERROR", err) }

  redirectProfile(c)
}

// Removes user information from the session and redirects the user.
func logout(c *gin.Context) {
  fmt.Println("------------")
  fmt.Println("LOGOUT")
  fmt.Println("------------")

  err := gothic.Logout(c.Writer, c.Request)
  if err != nil { fmt.Println("LOGOUT ERROR", err) }

  redirectIndex(c)
}

//
// AUTH FUNCTIONS
//

// Generates a random string.
// Adapted from source: https://github.com/markbates/goth/blob/master/gothic/gothic.go#L82-L91.
// Adapted from source: https://github.com/transcom/mymove/blob/defe4a5d91c3ed756ee243beea2050368015870f/pkg/auth/auth.go#L89.
// TODO: use crypto/rand instead: https://github.com/golang/go/wiki/CodeReviewComments#crypto-rand.
func generateNonce() string {
  nonceBytes := make([]byte, 64)
  random := rand.New(rand.NewSource(time.Now().UnixNano()))
  for i := 0; i < 64; i++ {
    nonceBytes[i] = byte(random.Int63() % 256)
  }
  return base64.URLEncoding.EncodeToString(nonceBytes)
}

// Assembles a custom authorization url, including login.gov-specific params.
// Because gothic.BeginAuthHandler(c.Writer, c.Request) ran into server errors about missing acr values, nonce, etc.
// Adapted from source: https://github.com/transcom/mymove/blob/defe4a5d91c3ed756ee243beea2050368015870f/pkg/auth/auth.go#L59
func loginGovAuthURL(session goth.Session, state string) (string, error)  {
  urlStr, err := session.GetAuthURL()
  if err != nil { return "", err}

  authURL, err := url.Parse(urlStr)
  if err != nil { return "", err}

  params := authURL.Query()
  params.Add("acr_values", "http://idmanagement.gov/ns/assurance/loa/1") //TODO: variable LOA 1 or 3
  params.Add("nonce", state)
  params.Set("scope", "openid email address phone profile:birthdate profile:name profile social_security_number")

  authURL.RawQuery = params.Encode()

  return authURL.String(), err
}

// Stores token information.
// See: https://developers.login.gov/oidc/#token-response.
type TokenResponse struct {
  AccessToken string `json:"access_token"`
  TokenType string `json:"token_type"`
  ExpiresIn int `json:"expires_in"`
  IDToken string `json:"id_token"`
}

// Issues a token request and returns a parsed token response.
// Because gothic.CompleteUserAuth is not working (most likely due to customization of the login.gov OIDC provider).
// See: https://developers.login.gov/oidc/#token.
func fetchToken(c *gin.Context) TokenResponse {
  tokenURL := providerUrl + "/api/openid_connect/token" // TODO: get this from provider.OpenidConfig after merge of https://github.com/markbates/goth/pull/207

  // Compile token request params...

  q:= c.Request.URL.Query()
  code := q["code"][0]
  //state := q["state"][0]

  clientAssertion, err := generateJWT(tokenURL)
  if err != nil {fmt.Println("CLIENT ASSERTION ERROR") }

  tokenParams := url.Values{}
  tokenParams.Set("client_assertion", clientAssertion)
  tokenParams.Set("client_assertion_type", "urn:ietf:params:oauth:client-assertion-type:jwt-bearer")
  tokenParams.Set("code", code)
  tokenParams.Set("grant_type", "authorization_code")

  // Issue token request...

  resp, err := http.PostForm(tokenURL, tokenParams)
  if err != nil { fmt.Println("POST REQUEST ERROR") }
  fmt.Println("TOKEN RESPONSE:", reflect.TypeOf(resp), resp.Status)

  // Parse token response...

  defer resp.Body.Close()
  body, err := ioutil.ReadAll(resp.Body)
  if err != nil {fmt.Println("READ BYTES ERR", err) }

  var tr TokenResponse
  parseErr := json.Unmarshal(body, &tr)
  if parseErr != nil { fmt.Println("JSON UNMARSHAL ERROR", parseErr) }

  js, err := json.Marshal(tr)
  if err != nil { fmt.Println("JSON MARSHAL ERROR", err) }
  fmt.Println("TOKEN RESPONSE JSON:", string(js))

  //TODO: store token and state in session to enable RP-initiated logout

  return tr
}

// Generates a JSON Web Token (JWT) signed using a private key (PEM) file.
// ... adapted from source: https://github.com/transcom/mymove/blob/b6f98942d64d8d12f502bea36d26ad65a5d8cd18/pkg/auth/auth.go#L193
func generateJWT(tokenURL string) (string, error) {

  // Parse key file...

  pemPath := "keys/login-gov/sp_gin_demo.key"

  pem, err := ioutil.ReadFile(pemPath)
  if err != nil {
    fmt.Println("PEM READING ERROR")
    return "", err
  }

  key, err := jwt.ParseRSAPrivateKeyFromPEM(pem)
  if err != nil {
    fmt.Println("KEY PARSING ERROR")
    return "", err
  }

  // Generate new token...

  const sessionExpiryInMinutes = 10

  claims := &jwt.StandardClaims{
    Issuer: clientId,
    Subject: clientId,
    Audience: tokenURL,
    Id: generateNonce(),
    ExpiresAt: time.Now().Add(time.Minute * sessionExpiryInMinutes).Unix(),
  }

  token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)

  // Use the key to sign the token...

  jwt, err := token.SignedString(key)
  if err != nil {
    fmt.Println("KEY SIGNING ERROR")
    return "", err
  }

  return jwt, err
}

// Issues a user info request and returns a corresponding goth.User object.
// Because gothic.CompleteUserAuth is not working (most likely due to customization of the login.gov OIDC provider).
// See: https://developers.login.gov/oidc/#user-info.
func fetchUserInfo(c *gin.Context, tr TokenResponse) (goth.User, error) {
  session := openidConnect.Session{
    AccessToken: tr.AccessToken,
    ExpiresAt: time.Now().Add(time.Second * time.Duration(tr.ExpiresIn)),
    IDToken: tr.IDToken,
  } // TODO: use an existing goth session instead?

  provider, err := goth.GetProvider(providerName)
  if err != nil {
    fmt.Println("GET PROVIDER ERROR", err)
    return goth.User{}, err
  }

  gothUser, err := provider.FetchUser(&session)
  if err != nil {
    fmt.Println("FETCH USER ERROR", err)
    return goth.User{}, err
  }

  return gothUser, nil
}

// Stores user information to be passed to a gin template for easy attribute access.
type User struct {
  Email string `json:"email"`
  EmailVerified bool `json:"email_verified"`
  GivenName string `json:"given_name"`
  FamilyName string `json:"family_name"`
  SSN string `json:"social_security_number"`
  Address string `json:"address"`
  Phone string `json:"phone"`
  PhoneVerified string `json:"phone_verified"`

  //Sub string `json:"sub"` // "abc-def-123-xyz"
  //Iss string `json:"iss"` // "http://localhost:3000/"
  //Acr string `json:"acr"` // "http://idmanagement.gov/ns/assurance/loa/1"
  //Aud string `json:"aud"` // "urn:gov:gsa:openidconnect:sp:gin"
  //Exp int `json:"exp"` // 1519674084
  //Nonce string `json:"nonce"` // "abcdef123456"

  //iat int `json:"iat"`
  //jti string `json:"iat"`
  //nbf string `json:"nbf"`
  //atHash string `json:"at_hash"`
  //cHash string `json:"c_hash"`
}

// Retrieves user info from the session, then converts it into a more usable User object.
func getUserFromSession(c *gin.Context) (User, error) {

  // Get user info from the session...

  js, err := gothic.GetFromSession(userSessionKey, c.Request)
  if err != nil {
    fmt.Println("ERROR RETRIEVING USER FROM SESSION", err)
    return User{}, err
  }

  // Assemble a User object...

  var user User
  b := []byte(js) // convert JSON string into something that can be unmarshalled into a struct, bypasses "cannot use js (type string) as type []byte in argument to json.Unmarshal"
  parseErr := json.Unmarshal(b, &user)
  if parseErr != nil {
    fmt.Println("ERROR PARSING USER INFO FROM SESSION", err)
    return User{}, err
  }

  fmt.Println("PROFILE USER", reflect.TypeOf(user), user)
  //fmt.Println("...", user.Email, user.EmailVerified)
  //fmt.Println("...", user.Phone, user.PhoneVerified) // not availz w/ LOA1
  //fmt.Println("...", user.GivenName, user.FamilyName) // not availz w/ LOA1
  //fmt.Println("...", user.SSN, user.Address) // not availz w/ LOA1

  return user, nil
}
