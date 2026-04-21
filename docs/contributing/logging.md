# Logging Guidelines for Contributors

This document provides guidelines for contributors on how to use the centralized logging system in ArgoCD Agent.

## Quick Start

1. **Import the logging packages:**
   ```go
   import (
       "github.com/argoproj-labs/argocd-agent/internal/logging"
       "github.com/argoproj-labs/argocd-agent/internal/logging/logfields"
   )
   ```

2. **Use field constants instead of string literals:**
   ```go
   // ❌ DON'T - string literals
   logger.WithField("user_name", "john")
   
   // ✅ DO - field constants
   logger.WithField(logfields.Username, "john")
   ```

3. **Use specialized loggers when available:**
   ```go
   // ❌ DON'T - manual field construction
   logger := logrus.WithFields(logrus.Fields{
       "module": "AgentModule",
       "method": "HandleRequest", 
       "requestId": "123",
   })
   
   // ✅ DO - specialized logger
   logger := logging.RequestLogger("AgentModule", "HandleRequest", "123")
   ```

## Mandatory Rules

### 1. Field Names MUST Use Constants

**All logging field names MUST use constants from the `logfields` package.**

```go
// ❌ FORBIDDEN - will be rejected in code review
logger.WithField("user", username)
logger.WithField("req_id", requestID)

// ✅ REQUIRED
logger.WithField(logfields.Username, username)
logger.WithField(logfields.RequestID, requestID)
```

### 2. No Package-Level log() Functions

**Do not create new package-level `log()` functions. Use the centralized logging package.**

```go
// ❌ FORBIDDEN - will be rejected
func log() *logrus.Entry {
    return logrus.WithField("module", "MyModule")
}

// ✅ REQUIRED - use centralized logging
func log() *logrus.Entry {
    return logging.GetDefaultLogger().ModuleLogger("MyModule")
}
```

### 3. New Field Constants Must Be Added

**If you need a field that doesn't exist, add it to `logfields/logfields.go`:**

```go
// Add to logfields/logfields.go
const (
    // ... existing constants ...
    
    // MyNewField describes what this field represents
    MyNewField = "myNewField"
)
```

## Common Patterns

### Request Processing
```go
func HandleRequest(requestID, method string) {
    logger := logging.GetDefaultLogger().RequestLogger("MyModule", method, requestID)
    logger.Info("Starting request processing")
    
    // Add additional context as needed
    logger.WithField(logfields.Status, "processing").Debug("Request in progress")
    
    logger.WithField(logfields.Duration, "150ms").Info("Request completed")
}
```

### Kubernetes Resource Management
```go
func ProcessPod(namespace, name string) {
    logger := logging.GetDefaultLogger().KubernetesResourceLogger("PodProcessor", "Pod", namespace, name)
    logger.Info("Processing pod")
    
    logger.WithFields(logrus.Fields{
        logfields.ResourceVersion: pod.ResourceVersion,
        logfields.Status:          "ready",
    }).Info("Pod processed successfully")
}
```

### Error Handling
```go
func HandleError(err error) {
    logger := logging.GetDefaultLogger().ErrorLogger("MyModule", "ValidationError", err)
    logger.WithFields(logrus.Fields{
        logfields.ErrorCode: "E001",
        logfields.Reason:    "invalid input",
    }).Error("Request validation failed")
}
```

### gRPC Operations
```go
func HandleGRPCCall(method string) {
    logger := logging.GetDefaultLogger().GRPCLogger("GRPCHandler", method)
    logger.Info("gRPC call started")
    
    logger.WithFields(logrus.Fields{
        logfields.GRPCCode:    "OK",
        logfields.Duration:    "50ms",
        logfields.ClientAddr: clientAddr,
    }).Info("gRPC call completed")
}
```

## Adding New Field Constants

When you need a new field constant:

1. **Check if it already exists** in `logfields/logfields.go`
2. **Add it in the appropriate section** (group related fields)
3. **Use descriptive names** following the existing convention
4. **Add a comment** explaining what the field represents
5. **Use camelCase** for the constant name and the string value

```go
// Example of adding new constants
const (
    // Existing constants...
    
    // Database operations
    DatabaseName     = "databaseName"
    DatabaseHost     = "databaseHost"
    DatabaseQuery    = "databaseQuery"
    DatabaseDuration = "databaseDuration"
    
    // Cache operations  
    CacheKey         = "cacheKey"
    CacheHit         = "cacheHit"
    CacheTTL         = "cacheTTL"
)
```

## Full-Detail Logging Helpers

The logging package provides helpers for structured logging of resource actions, events, and K8s informer activity. Each helper emits `log_category` and conditionally includes a `detail` field when full-detail logging is enabled. The remaining structured fields vary by category:

- **Action helpers**: `action`, `resource_type`, `name`, `namespace`
- **Event helpers**: `direction`, `event_target`, `event_type`, `name`, `namespace` (parsed from CloudEvent subject)
- **Informer helpers**: `action`, `resource_type`, `name`, `namespace`

### Action Logging (resource create/update/delete)

Use these in resource manager code when a K8s resource is created, updated, or deleted:

```go
import "github.com/argoproj-labs/argocd-agent/internal/logging"

// On create - logs full JSON in detail when --full-detail=actions
logging.LogActionCreate(logCtx, "application", app)

// On update - logs diff in detail when --full-detail=actions (secrets skipped)
logging.LogActionUpdate(logCtx, "application", oldApp, newApp)

// On delete - no detail field (only name/namespace/type)
logging.LogActionDelete(logCtx, "application", app.Namespace, app.Name)

// On error - logs full JSON in detail when --full-detail=actions
logging.LogActionError(logCtx, "application", "create", app, err)
```

### Event Logging (CloudEvent sent/received)

Use these when sending or receiving CloudEvents between agent and principal:

```go
// On event sent
logging.LogEventSent(logCtx, cloudEvent)

// On event received
logging.LogEventReceived(logCtx, cloudEvent)

// On event processing error
logging.LogEventError(logCtx, cloudEvent, err)
```

Meta-events (`eventProcessed`, `heartbeat`, `clusterCacheInfoUpdate`) are automatically skipped by `LogEventSent` and `LogEventReceived` to reduce noise.

### Informer Logging (K8s informer add/update/delete)

Use these in informer callbacks:

```go
// On informer add
logging.LogInformerAdd(logCtx, obj)

// On informer update - logs diff in detail (secrets skipped)
logging.LogInformerUpdate(logCtx, oldObj, newObj)

// On informer delete
logging.LogInformerDelete(logCtx, obj)
```

### Security: Secret Redaction

All helpers automatically redact Kubernetes Secrets:

- `LogActionCreate` / `LogInformerAdd`: `detail` shows `<redacted: Secret>` instead of the payload
- `LogActionUpdate` / `LogInformerUpdate`: diff is skipped entirely for Secrets
- `LogActionError`: same redaction as create

## Code Review Checklist

Before submitting your PR, verify:

- [ ] All logging uses field constants instead of string literals
- [ ] No new package-level `log()` functions are created
- [ ] Appropriate specialized loggers are used where available
- [ ] New field constants are added to `logfields/logfields.go` if needed
- [ ] Field constants are properly documented
- [ ] Tests pass with the new logging code

## Examples of Common Mistakes

### ❌ Inconsistent Field Names
```go
// Different names for the same concept
logger.WithField("user_name", "john")
logger.WithField("username", "jane")
logger.WithField("user", "bob")
```

### ❌ String Literals
```go
// Using string literals instead of constants
logger.WithFields(logrus.Fields{
    "method": "ProcessApp",
    "app":    appName,
})
```

### ❌ Missing Context
```go
// Not enough context for debugging
logger.Info("Processing completed")
```

### ✅ Correct Approach
```go
logger := logging.GetDefaultLogger().ApplicationLogger("AppProcessor", appName)
logger.WithFields(logrus.Fields{
    logfields.Method:    "ProcessApp",
    logfields.Duration:  duration,
    logfields.Status:    "completed",
}).Info("Application processing completed")
```

## Testing Your Logging Code

Use the logging package's test utilities:

```go
func TestMyFunction(t *testing.T) {
    var buf bytes.Buffer
    err := logging.GetDefaultLogger().SetupLogging(logging.LogLevelDebug, logging.LogFormatJSON, &buf)
    require.NoError(t, err)
    
    // Call your function that logs
    MyFunction()
    
    // Verify the log output
    var logEntry map[string]interface{}
    err = json.Unmarshal(buf.Bytes(), &logEntry)
    require.NoError(t, err)
    
    assert.Equal(t, "expected-value", logEntry[logfields.SomeField])
}
```

## Getting Help

If you're unsure about:

- Which field constant to use
- Whether to create a new field constant
- Which specialized logger is appropriate
- How to structure your logging calls

Please ask in your PR or create an issue for discussion.

## Summary

Remember the key principles:

1. **Use field constants** - never string literals for field names
2. **Use centralized logging** - no package-level log() functions
3. **Add missing constants** - don't work around missing field constants
4. **Provide context** - include relevant information for debugging
5. **Test your logging** - verify the output is as expected

Following these guidelines ensures consistency, maintainability, and better debugging experience across the ArgoCD Agent codebase.
