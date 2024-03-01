package principal

import (
	"context"
	"fmt"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"github.com/jannfis/argocd-agent/internal/grpcutil"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

func InterceptorLogger(l logrus.FieldLogger) logging.Logger {
	return logging.LoggerFunc(func(_ context.Context, lvl logging.Level, msg string, fields ...any) {
		f := make(map[string]any, len(fields)/2)
		i := logging.Fields(fields).Iterator()
		if i.Next() {
			k, v := i.At()
			f[k] = v
		}
		l := l.WithFields(f)

		switch lvl {
		case logging.LevelDebug:
			l.Debug(msg)
		case logging.LevelInfo:
			l.Info(msg)
		case logging.LevelWarn:
			l.Warn(msg)
		case logging.LevelError:
			l.Error(msg)
		default:
			panic(fmt.Sprintf("unknown level %v", lvl))
		}
	})
}

func unaryRequestLogger() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
		log().WithFields(logrus.Fields{
			"method":      info.FullMethod,
			"client_addr": grpcutil.AddressFromContext(ctx),
		}).Debug("Processing unary gRPC request")
		return handler(ctx, req)
	}
}

func streamRequestLogger() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		log().WithFields(logrus.Fields{
			"method":      info.FullMethod,
			"client_addr": grpcutil.AddressFromContext(ss.Context()),
		}).Debug("Processing unary gRPC request")
		return handler(srv, ss)
	}
}
