# Roadmap

The first release covers trusted local execution, versioned JSON evidence, provider negotiation, and a real Dependency-Cruiser rule proof. See [current state](current-state.md) and [Dependency-Cruiser example](dependency-cruiser-example.md) for the verified boundary.

The next work completes and explains the controlled-experiment experience, ahead of new integrations.

1. Make experiments easier to trust and explain: record tool versions where a tool can report them, add a verification command that recomputes the documented digests from retained inputs, and keep runnable examples (see the [Go project](../examples/go-project/README.md)) matched to the reports.
2. Extend the single-mutant lifecycle after that contract is verified. Campaigns, parallel execution, caching, automatic test selection, scoring, and mutation-tool integrations remain future work.
3. Verify network, write, and process isolation before supporting untrusted workloads. Expand workspace scale and platform coverage separately.
4. Design the proposed `init`, `doctor`, `verify`, and `report` commands, report replay, and static-analysis SARIF export.
5. Only when a concrete need appears: verify a second real analyzer end to end, and define a host-neutral AI evaluation contract with an offline fixture before adding AI evaluation integrations or model-backed runs. The tools listed as [inspiration](provider-support.md) are not integration commitments.
