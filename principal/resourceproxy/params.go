package resourceproxy

// Params is a typed map for storing interceptor parameters.
// Note that the same restrictions apply as for standard maps, i.e. Params
// do not provide thread safety and should not be accessed concurrently.
type Params map[string]string

// Set sets param key to the given value
func (p Params) Set(key string, value string) {
	_, ok := p[key]
	if !ok {
		p[key] = value
	}
}

// Get retrieves the param with given key. If no such param exists, nil is
// returned.
func (p Params) Get(key string) string {
	v, ok := p[key]
	if !ok {
		return ""
	}
	return v
}

// Has returns true if the given param exists
func (p Params) Has(key string) bool {
	_, ok := p[key]
	return ok
}

// NewParams returns a new set of Params
func NewParams() Params {
	return make(Params)
}
