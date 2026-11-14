// Preserve the native JSON report while returning the analyzer's advised exit.
// Dependency-Cruiser probes plugins with a minimal report lacking this field.
export default function report(result) {
  return {
    output: JSON.stringify(result) + "\n",
    exitCode: result.summary.advisedExitCode ?? 0,
  };
}
