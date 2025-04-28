# Profiling
The argocd-agent uses [pprof](https://github.com/google/pprof) for collecting profiling data.
Profiling is by default disabled in both principal and agent and it will be enabled if `pprof-port` flag is set or environment variable `ARGOCD_PRINCIPAL_PPROF_PORT` in principal and `ARGOCD_AGENT_PPROF_PORT` in agent are set. 

The profiling data of principal can be seen at endpoint `http://localhost:6060/debug/pprof`, and for agent it is available at endpoint `http://localhost:6161/debug/pprof`, these would have list of endpoints for different type of profiling.
