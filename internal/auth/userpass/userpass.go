package userpass

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	"golang.org/x/crypto/bcrypt"

	"github.com/jannfis/argocd-agent/internal/auth"
	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/validation"
)

var _ auth.Method = &UserPassAuthentication{}

// ClientIDField is the name of the field in the Credentials containing the client ID
const ClientIDField = "clientid"

// ClientSecretField is the name of the field in the Credentials containing the client secret
const ClientSecretField = "clientsecret"

var errAuthFailed = errors.New("authentication failed")

// UserPassAuthentication implements a simple username/password authentication
// method.
//
// UserPassAuthentication does not store passwords in plain, but instead uses
// the bcrypt hashing algorithm.
//
// Needs to be initialized properly before being used.
// Use the NewUserPassAuthentication function to get a new instance.
type UserPassAuthentication struct {
	lock   sync.RWMutex
	userdb map[string]string
	dummy  []byte
}

// NewUserPassAuthentication creates a new instance of UserPassAuthentication
func NewUserPassAuthentication() *UserPassAuthentication {
	dummy, _ := bcrypt.GenerateFromPassword([]byte("bdf3fdc6da5b5029e83f3024858c3c1e6aa3d1e71fa09e4691212f7571b5a3e3"), bcrypt.DefaultCost)
	return &UserPassAuthentication{
		userdb: make(map[string]string),
		dummy:  dummy,
	}
}

// Authenticate takes the credentials in creds and tries to authenticate them.
func (a *UserPassAuthentication) Authenticate(creds auth.Credentials) (clientID string, autherr error) {
	incomingUsername, ok := creds[ClientIDField]
	if !ok {
		return "", fmt.Errorf("username is missing from credentials")
	}
	incomingPassword, ok := creds[ClientSecretField]
	if !ok {
		return "", fmt.Errorf("password is missing from credentials")
	}

	a.lock.RLock()
	realPassword, ok := a.userdb[incomingUsername]
	// We unlock explictly instead of using defer, because bcrypt is expensive
	// and takes a while to compute.
	a.lock.RUnlock()

	if !ok {
		// To make timing attacks a little more complex, we compare the given
		// password with our dummy hash.
		_ = bcrypt.CompareHashAndPassword(a.dummy, []byte(incomingPassword))
		return "", errAuthFailed
	}

	if err := bcrypt.CompareHashAndPassword([]byte(realPassword), []byte(incomingPassword)); err == nil {
		return incomingUsername, nil
	}

	return "", errAuthFailed
}

// UpsertUser adds or updates a user with username and password
func (a *UserPassAuthentication) UpsertUser(username, password string) {
	a.lock.Lock()
	defer a.lock.Unlock()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err == nil {
		a.userdb[username] = string(hash)
	} else {
		log().WithError(err).Warnf("unable to upsert user")
	}
}

// We actually support all current bcrypt variants
var clientSecretRe = regexp.MustCompile(`^\$2[abxy]\$[0-9]{2}.*`)

// LoadAuthDataFromFile loads the authentication data from the file at path.
// File must contain username/password pairs, where both tokens must be
// separated by colon. Usernames must be strings of length 32, containing
// only hexadecimal characters. Passwords must be passwd-style bcrypt hashes
// and indicate bcrypt version and cost.
//
// The file may also contain comments starting with either the '#' or '//'
// characters. Comments will be ignored.
//
// Returns nil on success, or an error indicating why the file couldn't be
// loaded.
func (a *UserPassAuthentication) LoadAuthDataFromFile(path string) error {
	newUserDB := make(map[string]string)
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("error loading user DB file: %w", err)
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	lno := 0
	for s.Scan() {
		lno += 1
		line := strings.TrimSpace(s.Text())
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		tok := strings.SplitN(line, ":", 2)
		if len(tok) != 2 {
			log().Warnf("Ignoring invalid entry: %s:%d", path, lno)
			continue
		}
		if errs := validation.IsDNS1123Label(tok[0]); len(errs) > 0 {
			log().Warnf("Client ID isn't valid: %s:%d", path, lno)
			continue
		}
		if !clientSecretRe.MatchString(tok[1]) {
			log().Warnf("Client secret isn't valid: %s:%d", path, lno)
			continue
		}
		if _, ok := newUserDB[tok[0]]; ok {
			log().Warnf("Client ID '%s' specified more than once", tok[0])
		}
		newUserDB[tok[0]] = tok[1]
	}

	a.lock.Lock()
	defer a.lock.Unlock()
	a.userdb = newUserDB

	return nil
}

func log() *logrus.Entry {
	return logrus.WithField("component", "AuthUserPass")
}
