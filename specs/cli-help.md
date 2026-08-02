# CLI Help

workgraph exposes built-in help so users can discover commands without opening
the repository documentation.

The root command supports equivalent help forms:

```text
workgraph help
workgraph -h
workgraph --help
```

Root help includes a short description, usage, all public top-level commands,
and a hint for requesting command-specific help. Internal daemon commands are
not user-facing and must not be listed.

Every public command and nested command group supports these equivalent forms:

```text
workgraph help <command> [subcommand ...]
workgraph <command> [subcommand ...] help
workgraph <command> [subcommand ...] -h
workgraph <command> [subcommand ...] --help
```

Command-group help lists its immediate subcommands. Leaf-command help includes
the exact invocation shape and a concise explanation of the command. Help is a
read-only operation, writes to standard output, and exits successfully.

An unknown help path exits with a usage error, identifies the unknown path, and
points back to `workgraph help`.
