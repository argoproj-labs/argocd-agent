module github.com/jannfis/argocd-agent

go 1.22

toolchain go1.22.3

require (
	github.com/argoproj/argo-cd/v2 v2.11.7
	github.com/cloudevents/sdk-go/binding/format/protobuf/v2 v2.15.2
	github.com/cloudevents/sdk-go/v2 v2.15.2
	github.com/golang-jwt/jwt/v5 v5.2.1
	github.com/google/uuid v1.6.0
	github.com/grpc-ecosystem/go-grpc-middleware/v2 v2.1.0
	github.com/prometheus/client_golang v1.19.1
	github.com/sirupsen/logrus v1.9.3
	github.com/spf13/cobra v1.8.1
	github.com/stretchr/testify v1.9.0
	github.com/wI2L/jsondiff v0.6.0
	golang.org/x/crypto v0.25.0
	golang.org/x/sync v0.8.0
	google.golang.org/genproto/googleapis/api v0.0.0-20240528184218-531527333157
	google.golang.org/grpc v1.65.0
	google.golang.org/protobuf v1.34.2
	gopkg.in/yaml.v3 v3.0.1
	k8s.io/apimachinery v0.26.11
	k8s.io/client-go v0.26.11
)

require (
	cloud.google.com/go/compute/metadata v0.3.0 // indirect
	dario.cat/mergo v1.0.0 // indirect
	github.com/Azure/go-ansiterm v0.0.0-20210617225240-d185dfc1b5a1 // indirect
	github.com/MakeNowJust/heredoc v1.0.0 // indirect
	github.com/Masterminds/semver/v3 v3.2.1 // indirect
	github.com/Microsoft/go-winio v0.6.1 // indirect
	github.com/ProtonMail/go-crypto v0.0.0-20230828082145-3c4c8a2d2371 // indirect
	github.com/argoproj/gitops-engine v0.7.1-0.20240715141605-18ba62e1f1fb // indirect
	github.com/argoproj/pkg v0.13.7-0.20230626144333-d56162821bd1 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/blang/semver/v4 v4.0.0 // indirect
	github.com/bmatcuk/doublestar/v4 v4.6.0 // indirect
	github.com/bombsimon/logrusr/v2 v2.0.1 // indirect
	github.com/bradleyfalzon/ghinstallation/v2 v2.6.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/chai2010/gettext-go v1.0.2 // indirect
	github.com/cloudflare/circl v1.3.7 // indirect
	github.com/cyphar/filepath-securejoin v0.2.4 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/docker/distribution v2.8.2+incompatible // indirect
	github.com/emicklei/go-restful/v3 v3.9.0 // indirect
	github.com/emirpasic/gods v1.18.1 // indirect
	github.com/evanphx/json-patch v5.9.0+incompatible // indirect
	github.com/exponent-io/jsonpath v0.0.0-20151013193312-d6023ce2651d // indirect
	github.com/fatih/camelcase v1.0.0 // indirect
	github.com/fvbommel/sortorder v1.0.1 // indirect
	github.com/go-errors/errors v1.4.2 // indirect
	github.com/go-git/gcfg v1.5.1-0.20230307220236-3a3c6141e376 // indirect
	github.com/go-git/go-billy/v5 v5.5.0 // indirect
	github.com/go-git/go-git/v5 v5.11.0 // indirect
	github.com/go-logr/logr v1.4.1 // indirect
	github.com/go-openapi/jsonpointer v0.19.6 // indirect
	github.com/go-openapi/jsonreference v0.20.1 // indirect
	github.com/go-openapi/swag v0.22.3 // indirect
	github.com/go-redis/cache/v9 v9.0.0 // indirect
	github.com/gobwas/glob v0.2.3 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang-jwt/jwt/v4 v4.5.0 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/btree v1.1.2 // indirect
	github.com/google/gnostic v0.6.9 // indirect
	github.com/google/go-cmp v0.6.0 // indirect
	github.com/google/go-github/v53 v53.2.0 // indirect
	github.com/google/go-querystring v1.1.0 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/google/shlex v0.0.0-20191202100458-e7afc7fbc510 // indirect
	github.com/gregjones/httpcache v0.0.0-20190611155906-901d90724c79 // indirect
	github.com/imdario/mergo v0.3.16 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jbenet/go-context v0.0.0-20150711004518-d14ea06fba99 // indirect
	github.com/jonboulle/clockwork v0.2.2 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/kballard/go-shellquote v0.0.0-20180428030007-95032a82bc51 // indirect
	github.com/kevinburke/ssh_config v1.2.0 // indirect
	github.com/klauspost/compress v1.16.5 // indirect
	github.com/liggitt/tabwriter v0.0.0-20181228230101-89fcab3d43de // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/mitchellh/go-wordwrap v1.0.0 // indirect
	github.com/moby/spdystream v0.2.0 // indirect
	github.com/moby/term v0.0.0-20220808134915-39b0c02b01ae // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/monochromegane/go-gitignore v0.0.0-20200626010858-205db1a8cc00 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.1.0-rc4 // indirect
	github.com/patrickmn/go-cache v2.1.0+incompatible // indirect
	github.com/peterbourgon/diskv v2.0.1+incompatible // indirect
	github.com/pjbgf/sha1cd v0.3.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/prometheus/client_model v0.5.0 // indirect
	github.com/prometheus/common v0.48.0 // indirect
	github.com/prometheus/procfs v0.12.0 // indirect
	github.com/redis/go-redis/v9 v9.0.5 // indirect
	github.com/robfig/cron/v3 v3.0.1 // indirect
	github.com/russross/blackfriday/v2 v2.1.0 // indirect
	github.com/sergi/go-diff v1.1.0 // indirect
	github.com/skeema/knownhosts v1.2.2 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	github.com/stretchr/objx v0.5.2 // indirect
	github.com/tidwall/gjson v1.17.1 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	github.com/vmihailenco/go-tinylfu v0.2.2 // indirect
	github.com/vmihailenco/msgpack/v5 v5.3.4 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	github.com/xanzy/ssh-agent v0.3.3 // indirect
	github.com/xlab/treeprint v1.1.0 // indirect
	go.starlark.net v0.0.0-20220328144851-d1966c6b9fcd // indirect
	go.uber.org/atomic v1.7.0 // indirect
	go.uber.org/multierr v1.6.0 // indirect
	go.uber.org/zap v1.19.0 // indirect
	golang.org/x/mod v0.17.0 // indirect
	golang.org/x/net v0.25.0 // indirect
	golang.org/x/oauth2 v0.20.0 // indirect
	golang.org/x/sys v0.22.0 // indirect
	golang.org/x/term v0.22.0 // indirect
	golang.org/x/text v0.16.0 // indirect
	golang.org/x/time v0.5.0 // indirect
	golang.org/x/tools v0.21.1-0.20240508182429-e35e4ccd0d2d // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240528184218-531527333157 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/warnings.v0 v0.1.2 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	k8s.io/api v0.26.11 // indirect
	k8s.io/apiextensions-apiserver v0.26.10 // indirect
	k8s.io/apiserver v0.26.11 // indirect
	k8s.io/cli-runtime v0.26.11 // indirect
	k8s.io/component-base v0.26.11 // indirect
	k8s.io/component-helpers v0.26.11 // indirect
	k8s.io/klog/v2 v2.100.1 // indirect
	k8s.io/kube-aggregator v0.26.4 // indirect
	k8s.io/kube-openapi v0.0.0-20230501164219-8b0f38b5fd1f // indirect
	k8s.io/kubectl v0.26.4 // indirect
	k8s.io/kubernetes v1.26.11 // indirect
	k8s.io/utils v0.0.0-20230220204549-a5ecb0141aa5 // indirect
	oras.land/oras-go/v2 v2.3.0 // indirect
	sigs.k8s.io/json v0.0.0-20221116044647-bc3834ca7abd // indirect
	sigs.k8s.io/kustomize/api v0.12.1 // indirect
	sigs.k8s.io/kustomize/kyaml v0.13.9 // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.4.1 // indirect
	sigs.k8s.io/yaml v1.4.0 // indirect
)

replace (
	k8s.io/api => k8s.io/api v0.26.11
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.26.11
	k8s.io/apimachinery => k8s.io/apimachinery v0.26.11
	k8s.io/apiserver => k8s.io/apiserver v0.26.11
	k8s.io/cli-runtime => k8s.io/cli-runtime v0.26.11
	k8s.io/client-go => k8s.io/client-go v0.26.11
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.26.11
	k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.26.11
	k8s.io/code-generator => k8s.io/code-generator v0.26.11
	k8s.io/component-base => k8s.io/component-base v0.26.11
	k8s.io/component-helpers => k8s.io/component-helpers v0.26.11
	k8s.io/controller-manager => k8s.io/controller-manager v0.26.11
	k8s.io/cri-api => k8s.io/cri-api v0.26.11
	k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.26.11
	k8s.io/dynamic-resource-allocation => k8s.io/dynamic-resource-allocation v0.26.11
	k8s.io/kms => k8s.io/kms v0.26.11
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.26.11
	k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.26.11
	k8s.io/kube-proxy => k8s.io/kube-proxy v0.26.11
	k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.26.11
	k8s.io/kubectl => k8s.io/kubectl v0.26.11
	k8s.io/kubelet => k8s.io/kubelet v0.26.11
	k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.26.11
	k8s.io/metrics => k8s.io/metrics v0.26.11
	k8s.io/mount-utils => k8s.io/mount-utils v0.26.11
	k8s.io/pod-security-admission => k8s.io/pod-security-admission v0.26.11
	k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.26.11
	k8s.io/sample-cli-plugin => k8s.io/sample-cli-plugin v0.26.11
	k8s.io/sample-controller => k8s.io/sample-controller v0.26.11
)
