# Roadmap

Invaris runs trusted local commands, writes versioned JSON reports, discovers providers, and tests a Dependency-Cruiser rule. See the [current implementation](current-state.md) and [Dependency-Cruiser example](dependency-cruiser-example.md) for details.

The next priorities are clearer reports and verifiable experiments:

1. Record tool versions when available, add a command to verify digests against retained inputs, and keep examples aligned with their reports. The [Go project](../examples/go-project/README.md) demonstrates the current results.
2. Extend mutation testing beyond one explicit replacement. Future work includes campaigns, parallel execution, caching, automatic test selection, scoring, and mutation-tool integrations.
3. Verify network, write, and process isolation before supporting untrusted workloads. Expand workspace scale and platform coverage separately.
4. Design the proposed `init`, `doctor`, `verify`, and `report` commands, report replay, and static-analysis SARIF export.
5. Add another analyzer when a project needs it. Before adding AI evaluation integrations or model-backed runs, define a tool-independent result format and test it with an offline fixture. The [inspiration list](provider-support.md) does not commit to specific integrations.
