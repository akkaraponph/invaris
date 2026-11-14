/** Rules for the Invaris Dependency-Cruiser example. */
module.exports = {
  forbidden: [
    {
      name: "no-forbidden-import",
      severity: "error",
      comment: "src/main.js must not depend on src/forbidden.js",
      from: { path: "^src/main\\.js$" },
      to: { path: "^src/forbidden\\.js$" },
    },
  ],
  options: { doNotFollow: { path: "node_modules" } },
};
