# Contribution Guidelines

We are grateful for all contributions to this project, and everybody is welcome to contribute to it in various ways. See [general contribution guidelines in README.md](https://github.com/argoproj-labs/argocd-agent/blob/main/README.md#contributing) for more details.

This document focuses on providing specifics for code/test/doc contribution guidelines.

## Code Contributions
- Code changes made in PRs must be fully exercised by unit tests and E2E tests. 
- New unit/E2E tests must be contributed for new functionality.
- Changes that add new features, or make significant changes to existing the functionality, should be documented within `doc/`
- Ensure that error and audit logging are in place. Error messages help users take action to recover from the error, without exposing personal data or sensitive information.
- Code is sufficiently commented to allow subsequent contributors to understand, maintain, and improve the code.
- Build/deployment/configuration changes have proper operational documentation, usually in the README.


## Tests

New unit tests should be contributed for new code, and modified for existing code. 

Unit tests should be named `Test_(FunctionName)`, where '(FunctionName)' is the name of the function under test, because most of the time a unit test revolves around a function. That's why you'll see some 'Test_lowerCamelCase' (for package-private functions) and some 'Test_UpperCamelCase' (for exported functions) names. Unit tests are within `*_test.go`, alongside the code being tested.

E2E tests should follow the same naming scheme, but `Test_(ScenarioName)`, where '(ScenarioName)' is the name of the scenario being test (e.g. 'Test_VerifyManagedAgentStatusIsSynchroned'), in CamelCase (beginning with a Capital). 

### Mocks

Mocks are generated via [mockery](https://github.com/vektra/mockery). The `make mocks` target within the `Makefile` will download the expected Mockery version (if it not available), and rebuild the mocks (if required).

## Logging

Argo CD Agent uses structured logging for logging. Code should use structured fields for all context-specific data, for example, for 'resourceID' and 'eventID', here:
```
logCtx.WithField("resource_id", event.resourceID(ev)).WithField("event_id", event.EventID(ev)).Trace(...)
```

Argo CD Agent logs at the following levels (ordered by decreasing severity):
- **Error**: Logs specific, non-fatal errors that have occured.
- **Warn**: Logs unexpected behaviour which may be an error in some cases, but might also be innocuous depending on context.
- **Info**: Logs actions taken by the agent, the reason for the action, and any useful context. May also log general context information related to the operating environment.
- **Debug**: Logs actions/context that are more verbose than info, and may include information that is only relevant to developers familiar with the code.
- **Trace**: Logs the most verbose events.

The *Error*, *Warn*, and *Info* level logs should be written with the expectation that non-developers (of argocd-agent) will be reading them. Support engineers and cluster administrators will often use error logs to help diagnose component issues, and it beneficial to those use cases to ensure logs are clear and actionable, where appropriate.

Generally, log statements should provide sufficient detail and context to allow readers to understand the agent behaviour and its context.
