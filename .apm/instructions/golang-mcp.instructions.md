### Go MCP

When the Go MCP server is available, use it first for Go-aware workspace
investigation and diagnostics:

- use `go_workspace` to understand the workspace layout and its modules;
- use `go_search` to find declarations by symbol name;
- use `go_file_context` to inspect a Go file's cross-file dependencies before
  changing it;
- use `go_package_api` to inspect the API of one or more packages;
- use `go_symbol_references` to find usages before changing a package-level
  symbol, field, or method;
- use `go_rename_symbol` for workspace-wide symbol renames and review the
  returned edits before applying them;
- use `go_diagnostics` after changing Go code, passing the absolute paths of
  changed files when targeted linting is useful;
- use `go_vulncheck` when changes affect dependencies, package reachability,
  or security-sensitive code.

Use the repository's normal CLI commands for formatting, code generation,
tests, builds, and any verification that has no applicable Go MCP tool. MCP
diagnostics complement rather than replace the repository's required checks.
