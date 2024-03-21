package userpass

import (
	"os"
	"testing"

	"github.com/jannfis/argocd-agent/internal/auth"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func Test_UpsertUser(t *testing.T) {
	a := NewUserPassAuthentication("")
	assert.Len(t, a.userdb, 0)
	t.Run("Add a new user", func(t *testing.T) {
		a.UpsertUser("user1", "password")
		assert.Len(t, a.userdb, 1)
		assert.Contains(t, a.userdb, "user1")
		assert.Regexp(t, `^\$2a\$10\$`, a.userdb["user1"])
	})
	t.Run("Add another new user", func(t *testing.T) {
		a.UpsertUser("user2", "password")
		assert.Len(t, a.userdb, 2)
		assert.Contains(t, a.userdb, "user1")
		assert.Contains(t, a.userdb, "user2")
		assert.Regexp(t, `^\$2a\$10\$`, a.userdb["user1"])
		assert.Regexp(t, `^\$2a\$10\$`, a.userdb["user2"])
	})
	t.Run("Update existing user", func(t *testing.T) {
		oldHash1 := a.userdb["user1"]
		oldHash2 := a.userdb["user2"]
		a.UpsertUser("user1", "wordpass")
		assert.Len(t, a.userdb, 2)
		assert.Contains(t, a.userdb, "user1")
		assert.Contains(t, a.userdb, "user2")
		assert.NotEqual(t, oldHash1, a.userdb["user1"])
		assert.Equal(t, oldHash2, a.userdb["user2"])
	})
}

func Test_Authenticate(t *testing.T) {
	a := NewUserPassAuthentication("")
	t.Run("Successful authentication", func(t *testing.T) {
		a.UpsertUser("user1", "password")
		creds := make(auth.Credentials)
		creds[ClientIDField] = "user1"
		creds[ClientSecretField] = "password"
		clientID, err := a.Authenticate(creds)
		assert.Equal(t, "user1", clientID)
		assert.NoError(t, err)
	})

	t.Run("Unknown user", func(t *testing.T) {
		a.UpsertUser("user1", "password")
		creds := make(auth.Credentials)
		creds[ClientIDField] = "user2"
		creds[ClientSecretField] = "password"
		clientID, err := a.Authenticate(creds)
		assert.Empty(t, clientID)
		assert.Error(t, err)
	})

	t.Run("Wrong password", func(t *testing.T) {
		creds := make(auth.Credentials)
		creds[ClientIDField] = "user1"
		creds[ClientSecretField] = "wordpass"
		clientID, err := a.Authenticate(creds)
		assert.Empty(t, clientID)
		assert.Error(t, err)
	})

	t.Run("Missing password", func(t *testing.T) {
		creds := make(auth.Credentials)
		creds[ClientIDField] = "user1"
		clientID, err := a.Authenticate(creds)
		assert.Empty(t, clientID)
		assert.Error(t, err)
	})

	t.Run("Missing username", func(t *testing.T) {
		creds := make(auth.Credentials)
		creds[ClientSecretField] = "password"
		clientID, err := a.Authenticate(creds)
		assert.Empty(t, clientID)
		assert.Error(t, err)
	})

}

func Test_LoadUserDB(t *testing.T) {
	a := NewUserPassAuthentication("")
	t.Run("Load good user DB", func(t *testing.T) {
		err := a.LoadAuthDataFromFile("testdata/userdb-good.txt")
		assert.NoError(t, err)
		assert.Len(t, a.userdb, 5)
	})

	t.Run("Load partial good user DB", func(t *testing.T) {
		err := a.LoadAuthDataFromFile("testdata/userdb-partial.txt")
		assert.NoError(t, err)
		assert.Len(t, a.userdb, 3)
	})

	t.Run("File not existing", func(t *testing.T) {
		err := a.LoadAuthDataFromFile("testdata/i-do-not-exist.txt")
		assert.ErrorIs(t, err, os.ErrNotExist)
		// a.userdb should not have been touched
		assert.Len(t, a.userdb, 3)
	})

}

func init() {
	logrus.SetLevel(logrus.TraceLevel)
}
