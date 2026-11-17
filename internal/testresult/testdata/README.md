# Test-result converter fixtures

Captured from the real tools on 2026-11-16 and then anonymised: the capture
directory is replaced by `/work` and the pytest hostname by `host`. Each sample
suite has one passing, one failing, one skipped and one nested test.

| File | Tool and command |
| --- | --- |
| `vitest.json`, `vitest-loadfail.json`, `vitest-hookfail.json` | vitest 3.2.4, `vitest run --reporter=json --outputFile=…` |
| `jest.json`, `jest-loadfail.json`, `jest-hookfail.json` | jest 30.0.4, `jest --json --outputFile=…` |
| `pytest-junit.xml`, `pytest-junit-error.xml` | pytest 8.4.1, `pytest --junitxml=…` (the error file adds a failing fixture) |
| `go-test.json`, `go-test-buildfail.json` | go 1.27.4, `go test -json ./...` |
| `go-test-timeout.json`, `go-test-mainexit.json` | go 1.27.4, `go test -json -timeout 2s` (captured 2026-11-17) |

The `*-loadfail` files are suites that failed to load (a syntax error), and
`go-test-buildfail.json` is a package that does not compile. The `*-hookfail`
files (captured 2026-11-17) pass their only test and then throw in `afterAll`,
so the tool reports the file failed with no failed assertion.
`go-test-timeout.json` is a package whose second test outlives `-timeout`, and
`go-test-mainexit.json` passes its test and then exits 1 from `TestMain`.
