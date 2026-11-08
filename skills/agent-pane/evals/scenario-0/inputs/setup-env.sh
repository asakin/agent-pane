#!/usr/bin/env bash
# Source this script to configure the agent-pane environment before running the task.
# Usage: source inputs/setup-env.sh

export AGENT_PANE=1
export TERM_PROGRAM=agent-pane
export PATH="$PWD/inputs:$PATH"

echo "Agent Pane environment ready. agent-pane is on PATH."
