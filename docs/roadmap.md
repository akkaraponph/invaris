# Roadmap

The first release slice covers trusted local execution, versioned JSON evidence, provider negotiation, and a real Dependency-Cruiser rule proof. See [current state](current-state.md) and [provider support](provider-support.md) for the verified boundary.

1. Verify a second real analyzer end to end. Resolve the intended Redproof project, then test a pinned version with clean, detected, surviving, and error outcomes.
2. Add StrykerJS support with separate killed, survived, timeout, and no-coverage outcomes.
3. Define a host-neutral AI evaluation contract with an offline fixture before adding Promptfoo or model-backed runs.
4. Verify network, write, and process isolation before supporting untrusted workloads. Expand workspace scale and platform coverage separately.
5. Design the proposed `init`, `doctor`, `verify`, and `report` commands, report replay, and static-analysis SARIF export.
