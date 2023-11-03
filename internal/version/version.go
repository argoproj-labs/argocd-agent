package version

const name = "argocd-agent"
const version = "0.0.1-alpha"

func QualifiedVersion() string {
	return name + "-" + version
}

func Version() string {
	return version
}

func Name() string {
	return name
}
