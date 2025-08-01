# Centralized Logging Package

This document describes the centralized logging package for ArgoCD Agent, which addresses the issue of inconsistent and scattered logging practices across the codebase.

## Overview

The centralized logging package provides:

1. **Consistent field naming** through centralized constants
2. **Specialized logger functions** for common use cases
3. **Elimination of per-package log() functions** in favor of centralized functionality
4. **Structured logging** with predefined field names
5. **Better maintainability** and consistency

## Package Structure

```
internal/logging/
├── logging.go              # Main logging package
├── logging_test.go         # Test suite
├── logfields/
│   └── logfields.go        # Centralized field name constants
└── examples/
    └── migration_examples.go # Migration examples
```

## Usage

### Basic Setup

```go
import "github.com/argoproj-labs/argocd-agent/internal/logging"

// Configure logging at application startup
err := logging.SetupLogging(
    logging.LogLevelInfo,
    logging.LogFormatJSON,
    nil, // Use default output (stdout)
)
if err != nil {
    panic(err)
}
```

### Replacing Package-Level log() Functions

**BEFORE:**
```go
func log() *logrus.Entry {
    return logrus.WithField("module", "Agent")
}
```

**AFTER:**
```go
import "github.com/argoproj-labs/argocd-agent/internal/logging"

func log() *logrus.Entry {
    return logging.ModuleLogger("Agent")
}
```

### Using Field Constants

**BEFORE:**
```go
logCtx := log().WithFields(logrus.Fields{
    "method":         "processRequest",
    "uuid":           requestID,
    "connectionUUID": connID,
})
```

**AFTER:**
```go
import (
    "github.com/argoproj-labs/argocd-agent/internal/logging/logfields"
)

logCtx := log().WithFields(logrus.Fields{
    logfields.Method:         "processRequest",
    logfields.UUID:           requestID,
    logfields.ConnectionUUID: connID,
})
```

### Specialized Logger Functions

The package provides specialized logger functions for common scenarios:

#### Request Logging
```go
logger := logging.RequestLogger("ModuleName", "MethodName", "request-id")
logger.Info("Processing request")
```

#### Kubernetes Resource Logging
```go
logger := logging.KubernetesResourceLogger("ModuleName", "Pod", "default", "my-pod")
logger.Info("Processing Kubernetes resource")
```

#### Redis Operations
```go
logger := logging.RedisLogger("ModuleName", "GET", "cache-key")
logger.Debug("Redis operation completed")
```

#### gRPC Operations
```go
logger := logging.GRPCLogger("ModuleName", "/service.Method")
logger.Info("gRPC call started")
```

#### HTTP Operations
```go
logger := logging.HTTPLogger("ModuleName", "POST", "/api/v1/endpoint")
logger.Info("HTTP request received")
```

#### Error Handling
```go
err := errors.New("connection failed")
logger := logging.ErrorLogger("ModuleName", "ConnectionError", err)
logger.Error("Error occurred")
```

#### Authentication
```go
logger := logging.AuthLogger("ModuleName", "username", "auth-method")
logger.Info("User authenticated")
```

## Available Field Constants

The `logfields` package provides constants for all common field names:

### Core Fields
- `logfields.Module`
- `logfields.Component`
- `logfields.Subsystem`
- `logfields.Method`

### Request/Response
- `logfields.RequestID`
- `logfields.UUID`
- `logfields.ConnectionUUID`
- `logfields.EventID`

### Networking
- `logfields.Direction`
- `logfields.ClientAddr`
- `logfields.ServerAddr`
- `logfields.RemoteAddr`

### ArgoCD Specific
- `logfields.Application`
- `logfields.AppProject`
- `logfields.Cluster`
- `logfields.Repository`

### Kubernetes
- `logfields.Kind`
- `logfields.Name`
- `logfields.Namespace`
- `logfields.ResourceVersion`

### Performance
- `logfields.Duration`
- `logfields.ResponseTime`
- `logfields.RequestCount`

And many more! See `logfields/logfields.go` for the complete list.

## Migration Guidelines

### 1. Replace Package log() Functions

For each package that has a `log() *logrus.Entry` function:

1. Add the import: `"github.com/argoproj-labs/argocd-agent/internal/logging"`
2. Replace the function implementation to use `logging.ModuleLogger()` or `logging.ComponentLogger()`

### 2. Replace String Literals with Constants

1. Add the import: `"github.com/argoproj-labs/argocd-agent/internal/logging/logfields"`
2. Replace all string literals in `WithField()` and `WithFields()` calls with the appropriate constants
3. If a field constant doesn't exist, add it to `logfields/logfields.go`

### 3. Use Specialized Loggers

Where appropriate, replace manual field construction with specialized logger functions that provide better context and consistency.

### 4. Validation

- Run tests to ensure no compilation errors
- Verify log output format and field names are consistent
- Check that all field names use constants instead of string literals

## Benefits

1. **Consistency**: All field names are centralized and consistent across the codebase
2. **Type Safety**: Constants prevent typos in field names
3. **Maintainability**: Easy to change field names globally
4. **Documentation**: Centralized field definitions serve as documentation
5. **IDE Support**: Better autocomplete and refactoring support
6. **Testing**: Easier to test logging behavior with consistent field names

## Best Practices

1. **Always use field constants** instead of string literals
2. **Choose appropriate specialized loggers** for your use case
3. **Add new field constants** when you need fields not covered by existing constants
4. **Use meaningful module/component names** when creating loggers
5. **Include context information** relevant to debugging and monitoring
6. **Follow structured logging practices** - use fields instead of formatting strings
