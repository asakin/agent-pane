# Agents

Each agent is a folder. The folder name is the id.

```
agents/<id>/
  agent.json
  icon.svg    # or icon.png if there is no svg
```

`agent.json` is the definition: `name`, `command`, and optional `args`. `command` is a name on `PATH`, an absolute path, or a path relative to the folder. A broken or missing `agent.json` is skipped.

`enabled.json` is the shipped on/off list. It is not part of an agent folder. An id that is not listed is off. A pull request that only adds a folder stays off until that id is added here. An id in this file with no folder, or a broken `agent.json`, is ignored.

`install.sh` copies this tree to `~/.local/share/agent-terminal/agents/` and replaces that copy on every run. It does not write `~/.config/agent-terminal/agents/`.

A folder in `~/.config/agent-terminal/agents/<id>/` replaces the shipped definition for that id. An `enabled.json` in the user directory is ignored. On and off for a person is `~/.config/agent-terminal/config.json`:

```json
{
  "agents": {
    "goose": { "enabled": false },
    "cursor-agent": { "enabled": true }
  }
}
```

If `config.json` has `agents.<id>.enabled`, that wins. Otherwise the shipped `enabled.json` value is used. If neither mentions the id, it is off.
