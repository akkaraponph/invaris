# Roadmap

Invaris runs trusted local commands, writes versioned JSON reports, discovers providers, and tests a Dependency-Cruiser rule. See the [current implementation](current-state.md) and [Dependency-Cruiser example](dependency-cruiser-example.md) for details.

The next priorities are clearer reports and verifiable experiments:

1. Record tool versions when available, add a command to verify digests against retained inputs, and keep examples aligned with their reports. The [Go project](../examples/go-project/README.md) demonstrates the current results.
2. Extend [campaigns](campaigns.md) beyond explicit replacements: caching, automatic test selection, and mutation-tool integrations. Baseline reuse across attempts was considered and declined; see the [design note](decisions/0001-campaign-engine.md#5-no-baseline-reuse).
3. Verify network, write, and process isolation before supporting untrusted workloads. Isolation is not provided today and will not be claimed until it is enforced and tested on each supported platform. Expand workspace scale and platform coverage separately.
4. Design the proposed `init`, `doctor`, `verify`, and `report` commands, report replay, and static-analysis SARIF export.
5. Add another analyzer when a project needs it. Before adding AI evaluation integrations or model-backed runs, define a tool-independent result format and test it with an offline fixture. The [inspiration list](provider-support.md) does not commit to specific integrations.
