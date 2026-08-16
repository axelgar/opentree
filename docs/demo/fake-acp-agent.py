#!/usr/bin/env python3
"""A scripted ACP agent, for the demo recording only.

seed-demo.sh installs this on PATH under the name `opencode`, ahead of any real
one, so `make demo` records the chat view without an API key, a network round
trip, or a model that says something different on every take. opentree spawns it
exactly as it spawns the real thing — `opencode acp --cwd <worktree>` — and
speaks the same protocol to it.

Why a fake rather than a recording: the chat view is a live client. Replaying a
transcript into a pane would show the frames but not the behaviour — the
permission prompt has to actually block until the tape answers it, the model
picker has to actually return a new option set. So this is a real ACP server
whose only fiction is that its answers were written in advance.

Wire shapes are copied from pkg/acp/testdata/, which is sanitized capture of
opencode v1.18.12. Two details there are load-bearing and easy to get wrong:

  * Framing is newline-delimited JSON, not LSP Content-Length headers
    (pkg/acp/acp.go writes `data + '\\n'` and reads with a json.Decoder).
  * Agent->client requests use an id space of their own, starting at 0, which
    overlaps the client's outbound ids. That collision is real and the client
    handles it; keeping it here is the point, not an accident.

Timings are wall-clock sleeps chosen to read well at 1x in a GIF. They are the
knobs to turn if the tape and the transcript drift apart.
"""

import json
import sys
import threading
import time

SESSION_ID = "ses_demo019f3a2c4e8b1d7a0"

# ---------------------------------------------------------------------------
# transport
# ---------------------------------------------------------------------------

_write_lock = threading.Lock()


def send(obj):
    """Write one JSON-RPC message. Locked: the script thread and the reader
    thread both emit, and an interleaved write is a parse error at the client."""
    with _write_lock:
        sys.stdout.write(json.dumps(obj) + "\n")
        sys.stdout.flush()


def respond(req_id, result):
    send({"jsonrpc": "2.0", "id": req_id, "result": result})


def update(payload):
    """session/update notification — the agent's only way to narrate a turn."""
    send({
        "jsonrpc": "2.0",
        "method": "session/update",
        "params": {"sessionId": SESSION_ID, "update": payload},
    })


# Agent->client request ids. Deliberately its own counter starting at 0; see
# the module docstring.
_next_req_id = 0
_pending = {}
_pending_lock = threading.Lock()


def request(method, params, timeout=120):
    """Call the client and block until it answers."""
    global _next_req_id
    with _pending_lock:
        req_id = _next_req_id
        _next_req_id += 1
        done = threading.Event()
        _pending[req_id] = [done, None]
    send({"jsonrpc": "2.0", "id": req_id, "method": method, "params": params})
    done.wait(timeout)
    with _pending_lock:
        return _pending.pop(req_id)[1]


# ---------------------------------------------------------------------------
# agent-declared controls
#
# The chat renders these as the flags beside the input, and drives them with
# /model, /mode and shift+tab. Changing one returns the whole set, because for a
# real agent picking a model narrows which effort levels exist.
# ---------------------------------------------------------------------------

CONFIG = [
    {
        "id": "model", "name": "Model", "category": "model", "type": "select",
        "currentValue": "anthropic/claude-opus-4.5",
        "options": [
            {"value": "anthropic/claude-opus-4.5", "name": "Anthropic/Claude Opus 4.5"},
            {"value": "anthropic/claude-sonnet-4.6", "name": "Anthropic/Claude Sonnet 4.6"},
            {"value": "anthropic/claude-haiku-4.5", "name": "Anthropic/Claude Haiku 4.5"},
        ],
    },
    {
        "id": "effort", "name": "Effort", "category": "thought_level", "type": "select",
        "description": "Available effort levels for this model",
        "currentValue": "medium",
        "options": [
            {"value": "low", "name": "Low"},
            {"value": "medium", "name": "Medium"},
            {"value": "high", "name": "High"},
        ],
    },
    {
        "id": "mode", "name": "Session Mode", "category": "mode", "type": "select",
        "currentValue": "build",
        "options": [
            {"value": "build", "name": "build",
             "description": "The default agent. Executes tools based on configured permissions."},
            {"value": "plan", "name": "plan",
             "description": "Plan mode. Disallows all edit tools."},
        ],
    },
]

# The prompt-level skills opencode advertises. These populate the `/` palette,
# and are what the skills tab checks the agent's own list against.
COMMANDS = [
    {"name": "code-review", "description": "Review changes along Standards and Spec axes."},
    {"name": "diagnosing-bugs", "description": "Diagnosis loop for hard bugs and performance regressions."},
    {"name": "tdd", "description": "Test-driven development — red, green, refactor."},
    {"name": "domain-modeling", "description": "Build and sharpen the project's domain model."},
    {"name": "resolving-merge-conflicts", "description": "Resolve an in-progress merge or rebase conflict."},
    {"name": "writing-for-agents", "description": "Writing skills, AGENTS.md and CLAUDE.md."},
]


def stream_text(text, kind="agent_message_chunk", chunk=3, delay=0.045):
    """Emit text a few words at a time, the way a model streams."""
    words = text.split(" ")
    msg_id = "msg_demo%04x" % (abs(hash(text)) % 0xFFFF)
    for i in range(0, len(words), chunk):
        piece = " ".join(words[i:i + chunk])
        if i + chunk < len(words):
            piece += " "
        update({
            "sessionUpdate": kind,
            "messageId": msg_id,
            "content": {"type": "text", "text": piece},
        })
        time.sleep(delay)


VIEW_GO = "pkg/tui/view.go"


def scripted_turn(cwd):
    """The one turn the demo records, start to finish."""
    time.sleep(0.4)

    # 1. reasoning — shown when the chat has reasoning switched on (ctrl+o)
    stream_text(
        "The badge is derived from status-file freshness, so a stale file and a "
        "live pane disagree. Checking how the switch resolves that.",
        kind="agent_thought_chunk", chunk=4, delay=0.05,
    )
    time.sleep(0.5)

    # 2. read the file
    read_id = "toolu_demo01read"
    update({"sessionUpdate": "tool_call", "toolCallId": read_id, "title": "read",
            "kind": "read", "status": "pending", "locations": [], "rawInput": {}})
    time.sleep(0.5)
    update({"sessionUpdate": "tool_call_update", "toolCallId": read_id,
            "status": "in_progress", "kind": "read", "title": VIEW_GO,
            "locations": [{"path": f"{cwd}/{VIEW_GO}"}],
            "rawInput": {"filePath": f"{cwd}/{VIEW_GO}"}})
    time.sleep(1.3)
    update({"sessionUpdate": "tool_call_update", "toolCallId": read_id,
            "status": "completed", "title": VIEW_GO,
            "content": [{"type": "content", "content": {
                "type": "text",
                "text": "badgeFor() falls through to \"working\" whenever a pane is\n"
                        "attached, even if the status file went stale 20m ago."}}]})
    time.sleep(0.8)

    stream_text("Found it — `badgeFor` treats an attached pane as proof of life, "
                "so a stalled agent keeps a green badge. Adding the age check:")
    time.sleep(0.5)

    # 3. edit, carrying a real diff — this is what the chat renders inline
    edit_id = "toolu_demo02edit"
    update({"sessionUpdate": "tool_call", "toolCallId": edit_id, "title": "edit",
            "kind": "edit", "status": "pending",
            "locations": [{"path": f"{cwd}/{VIEW_GO}"}], "rawInput": {}})
    time.sleep(1.1)
    update({
        "sessionUpdate": "tool_call_update", "toolCallId": edit_id,
        "status": "completed", "title": VIEW_GO,
        "content": [
            {"type": "content", "content": {"type": "text", "text": "Edit applied successfully."}},
            {"type": "diff", "path": f"{cwd}/{VIEW_GO}",
             "oldText": "\tif paneFresh(w) {\n\t\treturn badgeWorking\n\t}\n",
             "newText": "\tif paneFresh(w) && age < staleAfter {\n\t\treturn badgeWorking\n\t}\n"
                        "\tif age >= staleAfter {\n\t\treturn badgeStalled.withAge(age)\n\t}\n"},
        ],
    })
    time.sleep(1.0)

    stream_text("Now let me run the package tests to confirm nothing else "
                "depended on the old behaviour.")
    time.sleep(0.4)

    # 4. the permission prompt — a real request that blocks until the tape answers
    test_id = "toolu_demo03exec"
    update({"sessionUpdate": "tool_call", "toolCallId": test_id,
            "title": "go test ./pkg/tui/", "kind": "execute", "status": "pending",
            "locations": [], "rawInput": {"command": "go test ./pkg/tui/"}})
    time.sleep(0.3)

    answer = request("session/request_permission", {
        "sessionId": SESSION_ID,
        "toolCall": {
            "toolCallId": test_id, "title": "go test ./pkg/tui/", "kind": "execute",
            "status": "pending", "locations": [],
            "rawInput": {"command": "go test ./pkg/tui/"},
        },
        "options": [
            {"optionId": "once", "kind": "allow_once", "name": "Allow once"},
            {"optionId": "always", "kind": "allow_always", "name": "Always allow"},
            {"optionId": "reject", "kind": "reject_once", "name": "Reject"},
        ],
    })

    rejected = False
    if isinstance(answer, dict):
        outcome = answer.get("outcome", {})
        if isinstance(outcome, dict):
            rejected = outcome.get("optionId") == "reject"

    if rejected:
        update({"sessionUpdate": "tool_call_update", "toolCallId": test_id,
                "status": "failed", "title": "go test ./pkg/tui/",
                "content": [{"type": "content", "content": {
                    "type": "text", "text": "Rejected by user."}}]})
        stream_text("Understood — leaving the tests to you.")
        return "end_turn"

    update({"sessionUpdate": "tool_call_update", "toolCallId": test_id,
            "status": "in_progress", "kind": "execute", "title": "go test ./pkg/tui/"})
    time.sleep(1.8)
    update({"sessionUpdate": "tool_call_update", "toolCallId": test_id,
            "status": "completed", "title": "go test ./pkg/tui/",
            "content": [{"type": "content", "content": {
                "type": "text",
                "text": "ok  \tgithub.com/axelgar/opentree/pkg/tui\t0.81s\n253 passed"}}]})
    time.sleep(0.7)

    stream_text("Done. `badgeFor` now ages out an attached pane, so a quiet agent "
                "shows *stalled* with its age instead of a green badge. All 253 "
                "tests in the package still pass.")

    update({"sessionUpdate": "usage_update", "used": 18422, "size": 1000000,
            "cost": {"amount": 0.0731, "currency": "USD"}})
    return "end_turn"


# ---------------------------------------------------------------------------
# dispatch
# ---------------------------------------------------------------------------

def handle(msg, cwd):
    method, req_id = msg.get("method"), msg.get("id")

    if method == "initialize":
        respond(req_id, {
            "protocolVersion": 1,
            "agentCapabilities": {
                "loadSession": True,
                "promptCapabilities": {"embeddedContext": True, "image": True},
                "sessionCapabilities": {"close": {}, "list": {}, "resume": {}},
            },
            "authMethods": [],
            "agentInfo": {"name": "OpenCode", "version": "1.18.12"},
        })
        return

    if method == "session/new":
        respond(req_id, {"sessionId": SESSION_ID, "configOptions": CONFIG})
        # The skills the agent will actually reach for, announced once the
        # session exists.
        time.sleep(0.2)
        update({"sessionUpdate": "available_commands_update", "availableCommands": COMMANDS})
        return

    if method in ("session/load", "session/resume"):
        respond(req_id, {"configOptions": CONFIG})
        return

    if method == "session/list":
        respond(req_id, {"sessions": [{"sessionId": SESSION_ID, "title": "agent liveness badge"}]})
        return

    if method == "session/set_config_option":
        params = msg.get("params") or {}
        for opt in CONFIG:
            if opt["id"] == params.get("configId"):
                opt["currentValue"] = params.get("value", opt["currentValue"])
        respond(req_id, {"configOptions": CONFIG})
        return

    if method == "session/prompt":
        def run():
            try:
                reason = scripted_turn(cwd)
            except Exception:  # a demo agent that dies mid-take is worse than one that stops
                reason = "end_turn"
            respond(req_id, {
                "stopReason": reason,
                "usage": {"inputTokens": 12, "outputTokens": 486, "totalTokens": 18422,
                          "cachedReadTokens": 17904, "cachedWriteTokens": 32},
            })
        threading.Thread(target=run, daemon=True).start()
        return

    if req_id is not None:
        respond(req_id, {})


def main():
    cwd = "."
    argv = sys.argv[1:]
    if "--cwd" in argv:
        cwd = argv[argv.index("--cwd") + 1]

    decoder = json.JSONDecoder()
    buf = ""
    for line in sys.stdin:
        buf += line
        while buf.strip():
            try:
                msg, end = decoder.raw_decode(buf.lstrip())
            except ValueError:
                break  # partial message; wait for more input
            buf = buf.lstrip()[end:]

            if msg.get("method"):
                handle(msg, cwd)
            else:  # a response to one of our own requests
                rid = msg.get("id")
                with _pending_lock:
                    slot = _pending.get(rid)
                    if slot:
                        slot[1] = msg.get("result")
                        slot[0].set()


if __name__ == "__main__":
    try:
        main()
    except (BrokenPipeError, KeyboardInterrupt):
        pass
