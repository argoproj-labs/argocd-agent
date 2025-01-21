package resourceproxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_NewProxy(t *testing.T) {
	t.Run("It can be instantiated without options", func(t *testing.T) {
		p, err := New("127.0.0.1:8080")
		assert.NoError(t, err)
		assert.NotNil(t, p)

		// Interceptors should be empty
		assert.Len(t, p.interceptors, 0)
	})

	t.Run("It requires a valid listener address", func(t *testing.T) {
		p, err := New("127.0.0.1")
		assert.ErrorContains(t, err, "invalid listener")
		assert.Nil(t, p)
	})
}

func Test_proxyHandler(t *testing.T) {
	t.Run("It routes to the proxy", func(t *testing.T) {
		p, err := New("127.0.0.1:8080")
		require.NoError(t, err)
		require.NotNil(t, p)
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/test", nil)
		p.proxyHandler(rec, r)
		assert.Equal(t, http.StatusBadRequest, rec.Result().StatusCode)
	})
	t.Run("It intercepts correctly", func(t *testing.T) {
		intercepted := false
		p, err := New("127.0.0.1:8080",
			WithRequestMatcher("^/test/foo$", []string{"get"}, func(w http.ResponseWriter, r *http.Request, params Params) {
				intercepted = true
				log().Tracef("Writing OK")
				w.WriteHeader(http.StatusOK)
			}),
		)
		require.NoError(t, err)
		require.NotNil(t, p)
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/test", nil)
		p.proxyHandler(rec, r)
		assert.Equal(t, http.StatusBadRequest, rec.Result().StatusCode)

		rec = httptest.NewRecorder()
		r = httptest.NewRequest(http.MethodGet, "/test/foo", nil)
		p.proxyHandler(rec, r)
		assert.True(t, intercepted)
		assert.Equal(t, http.StatusOK, rec.Result().StatusCode)

		rec = httptest.NewRecorder()
		r = httptest.NewRequest(http.MethodGet, "/test/foo/bar", nil)
		p.proxyHandler(rec, r)
		assert.Equal(t, http.StatusBadRequest, rec.Result().StatusCode)

	})
}

func Test_Start(t *testing.T) {
	t.Run("Start IPv4", func(t *testing.T) {
		r, err := New("127.0.0.1:0")
		require.NoError(t, err)
		errch, err := r.Start(context.TODO())
		assert.NoError(t, err)
		r.Stop(context.TODO())
		assert.ErrorIs(t, <-errch, http.ErrServerClosed)
	})
	t.Run("Start IPv6", func(t *testing.T) {
		r, err := New("[::1]:0")
		require.NoError(t, err)
		errch, err := r.Start(context.TODO())
		assert.NoError(t, err)
		r.Stop(context.TODO())
		assert.ErrorIs(t, <-errch, http.ErrServerClosed)
	})
}

func init() {
	logrus.SetLevel(logrus.TraceLevel)
}
