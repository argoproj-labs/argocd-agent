package resourceproxy

import (
	"testing"

	"github.com/argoproj-labs/argocd-agent/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/rest"
)

func Test_WithRestConfig(t *testing.T) {
	crt := testutil.MustReadFile("../tlsutil/testdata/001_test_cert.pem")
	key := testutil.MustReadFile("../tlsutil/testdata/001_test_key.pem")
	t.Run("Invalid TLS in REST config", func(t *testing.T) {
		rp := &ResourceProxy{}
		rc := &rest.Config{
			TLSClientConfig: rest.TLSClientConfig{},
		}
		o := WithRestConfig(rc)
		err := o(rp)
		require.ErrorContains(t, err, "invalid TLS config")
	})
	t.Run("Invalid host in REST config", func(t *testing.T) {
		rp := &ResourceProxy{}
		rc := &rest.Config{
			TLSClientConfig: rest.TLSClientConfig{
				CertData: crt,
				KeyData:  key,
			},
			Host: "//http//habibi",
		}
		o := WithRestConfig(rc)
		err := o(rp)
		require.ErrorContains(t, err, "invalid upstream server")
	})
	t.Run("Set REST config", func(t *testing.T) {
		rp := &ResourceProxy{}
		rc := &rest.Config{
			TLSClientConfig: rest.TLSClientConfig{
				CertData: crt,
				KeyData:  key,
			},
			Host: "https://some.host:6433",
		}
		o := WithRestConfig(rc)
		err := o(rp)
		assert.NoError(t, err)
	})
}
