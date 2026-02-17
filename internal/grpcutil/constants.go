package grpcutil

const (
	// DefaultGRPCMaxMessageSize is the default maximum size that we will allow an incoming GRPC message to be. The value chosen here is 200MB default, which is equivalent to that used by upstream Argo CD. My expectation is that we will never get messages that are anywhere near this value. We may wish to reduce this constant once we have sufficient data from the field.
	DefaultGRPCMaxMessageSize = 200 * 1024 * 1024
)
