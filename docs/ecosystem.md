# Open-source ecosystem

Invaris is a local harness that runs checks and verifies that a selected guard detects an expected violation after a controlled file change. This catalog groups independent open-source projects by the work they help with. For a tested adapter, tool version, and platform, see [Dependency-Cruiser example](dependency-cruiser-example.md).

Use these links to explore related tools. Check each repository's license and notices before reuse. Listing a project does not imply an Invaris integration or affiliation.

## API and integration verification

- [Pact JS](https://github.com/pact-foundation/pact-js)
- [Schemathesis](https://github.com/schemathesis/schemathesis)
- [Testcontainers for Go](https://github.com/testcontainers/testcontainers-go)

## Architecture and dependency guardrails

- [ArchUnit](https://github.com/TNG/ArchUnit)
- [dependency-cruiser](https://github.com/sverweij/dependency-cruiser)
- [Evolith Core](https://github.com/beyondnetcode/evolith_arch32)
- [Redproof](https://github.com/schalermthai/redproof)

## Fault injection and chaos engineering

- [Chaos Mesh](https://github.com/chaos-mesh/chaos-mesh)
- [LitmusChaos](https://github.com/litmuschaos/litmus)
- [Pumba](https://github.com/alexei-led/pumba)
- [Toxiproxy](https://github.com/Shopify/toxiproxy)

## Mutation and property-based testing

- [gomutants](https://github.com/gomutants/gomutants)
- [Gopter](https://github.com/leanovate/gopter)
- [Gremlins](https://github.com/go-gremlins/gremlins)
- [Hypothesis](https://github.com/HypothesisWorks/hypothesis)
- [StrykerJS](https://github.com/stryker-mutator/stryker-js)

## Policy and configuration enforcement

- [Conftest](https://github.com/open-policy-agent/conftest)
- [Gatekeeper](https://github.com/open-policy-agent/gatekeeper)
- [Kyverno](https://github.com/kyverno/kyverno)
- [Open Policy Agent](https://github.com/open-policy-agent/opa)

## Security and compliance guardrails

- [Checkov](https://github.com/bridgecrewio/checkov)
- [OSV-Scanner](https://github.com/google/osv-scanner)
- [Semgrep](https://github.com/semgrep/semgrep)
- [Trivy](https://github.com/aquasecurity/trivy)
- [ZAP by Checkmarx](https://github.com/zaproxy/zaproxy)

## Evaris compatibility

[Evaris](https://github.com/refactorroom/evaris) can run `invaris check` or a campaign as an optional verifier. This table is kept identical in Evaris `docs/invaris.md`:

| Evaris | Invaris build | `invaris --version` | Harness / evidence read | Status |
| --- | --- | --- | --- | --- |
| 0.4.0 | `ee92f3c` (pre-v0.2.0) | none (legacy) | `invaris.dev/v1alpha2` / `invaris.evidence/v1alpha2` | Previously tested in Evaris CI |
| 0.4.0 | v0.3.0 | none (legacy) | same | Previously tested in Evaris CI |
| 0.4.0 | v0.4.0 | none (legacy) | same | Previously tested in Evaris CI |
| 0.5.0 | v0.5.0 | `invaris v0.5.0` | same; findings may carry `line` | Pinned in Linux CI |
| 0.4.0 and later | later 0.5.x | `invaris v0.5.Z` | same | Accepted |
| 0.4.0 | development build | `invaris dev` or a Go pseudo-version | same | Accepted and recorded |
| 0.4.0 | any other release | `invaris vX.Y.Z` | not tested | `UNAVAILABLE`, `UNSUPPORTED_TOOL_VERSION` |
| 0.3.0 and earlier | `ee92f3c` only | not checked | same, without `line` | Rejects findings that carry `line` |
