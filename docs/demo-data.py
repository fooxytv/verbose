#!/usr/bin/env python3
"""Build a synthetic ~/.claude tree for the verbose demo recordings.

Everything here is invented. The repo is public, so the demo must not show real
session titles, client project names, file paths, code or costs.
"""
import json, os, shutil
from datetime import datetime, timedelta, timezone

HOME = "/tmp/devdemo"
# The cwd must really exist for the tree to have anything to read, and the
# directory name is what the project is called, so: no hyphens to decode badly.
CWD = "/tmp/devdemo/notestore"
PROJECT_DIR = os.path.join(HOME, ".claude", "projects", "-tmp-devdemo-notestore")
SESSION = "7c3f1a90-2b4d-4e8a-9f61-0d5c8e2a4b77"
AGENT = "a7f2c91e4b6d80a35"

# Relative to now: a recording made months later must not show "209d ago".
NOW = datetime.now(timezone.utc)
t0 = NOW - timedelta(hours=2, minutes=40)
clock = {"n": 0}


def ts():
    clock["n"] += 1
    return (t0 + timedelta(seconds=clock["n"] * 7)).isoformat().replace("+00:00", "Z")


USAGE = {"input_tokens": 4120, "output_tokens": 310,
         "cache_read_input_tokens": 38000, "cache_creation_input_tokens": 900}

lines = []


def user(text):
    lines.append({"type": "user", "uuid": f"u{clock['n']}", "sessionId": SESSION,
                  "cwd": CWD, "gitBranch": "main", "timestamp": ts(),
                  "message": {"role": "user", "content": text}})


def say(text):
    lines.append({"type": "assistant", "uuid": f"a{clock['n']}", "sessionId": SESSION,
                  "cwd": CWD, "gitBranch": "main", "timestamp": ts(),
                  "message": {"id": f"msg_{clock['n']}", "role": "assistant",
                              "model": "claude-opus-4-5", "usage": USAGE,
                              "content": [{"type": "text", "text": text}]}})


def tool(tid, name, inp):
    lines.append({"type": "assistant", "uuid": f"a{clock['n']}", "sessionId": SESSION,
                  "cwd": CWD, "gitBranch": "main", "timestamp": ts(),
                  "message": {"id": f"msg_{clock['n']}", "role": "assistant",
                              "model": "claude-opus-4-5", "usage": USAGE,
                              "content": [{"type": "tool_use", "id": tid,
                                           "name": name, "input": inp}]}})


def result(tid, payload, text="ok"):
    lines.append({"type": "user", "uuid": f"u{clock['n']}", "sessionId": SESSION,
                  "cwd": CWD, "timestamp": ts(), "toolUseResult": payload,
                  "message": {"role": "user", "content": [
                      {"type": "tool_result", "tool_use_id": tid, "content": text}]}})


def hunk(os_, ol, ns, nl, ls):
    return {"oldStart": os_, "oldLines": ol, "newStart": ns, "newLines": nl, "lines": ls}


# ----------------------------------------------------------------- the session
user("The notes API returns 500 when a note is missing instead of 404, and there "
     "are no tests around the store. Can you fix the status code and add coverage?")

say("I'll look at how the handler reports a missing note before changing anything.")

tool("t1", "Read", {"file_path": f"{CWD}/internal/http/handler.go"})
result("t1", {"file": {"filePath": f"{CWD}/internal/http/handler.go",
                       "numLines": 84, "startLine": 1, "totalLines": 84}})

tool("t2", "Grep", {"pattern": "ErrNotFound", "path": "internal"})
result("t2", {"stdout": "internal/store/store.go:17:var ErrNotFound = errors.New(\"note not found\")\n",
              "stderr": "", "interrupted": False})

say("The store already returns a typed error; the handler just never checks for "
    "it. Mapping it to 404 is the fix.")

tool("t3", "Edit", {"file_path": f"{CWD}/internal/http/handler.go"})
result("t3", {"filePath": f"{CWD}/internal/http/handler.go", "structuredPatch": [
    hunk(41, 7, 41, 11, [
        " func (s *Server) getNote(w http.ResponseWriter, r *http.Request) {",
        " \tnote, err := s.store.Get(r.Context(), chi.URLParam(r, \"id\"))",
        " \tif err != nil {",
        "-\t\thttp.Error(w, err.Error(), http.StatusInternalServerError)",
        "+\t\tif errors.Is(err, store.ErrNotFound) {",
        "+\t\t\thttp.Error(w, \"note not found\", http.StatusNotFound)",
        "+\t\t\treturn",
        "+\t\t}",
        "+\t\thttp.Error(w, \"internal error\", http.StatusInternalServerError)",
        " \t\treturn",
        " \t}",
    ])]})

say("Now the tests. I'll cover the missing-note path and a round trip.")

tool("t4", "Write", {"file_path": f"{CWD}/internal/http/handler_test.go",
                     "content": "package http\n\nimport (\n\t\"net/http\"\n\t\"testing\"\n)\n\n"
                                "func TestGetMissingNoteIs404(t *testing.T) {\n"
                                "\tsrv := newTestServer(t)\n"
                                "\tres := srv.get(\"/notes/does-not-exist\")\n"
                                "\tif res.Code != http.StatusNotFound {\n"
                                "\t\tt.Errorf(\"status = %d, want 404\", res.Code)\n\t}\n}\n"})
result("t4", {"filePath": f"{CWD}/internal/http/handler_test.go", "structuredPatch": []})

tool("t5", "Bash", {"command": "go test ./internal/... 2>&1 | tail -20",
                    "description": "Run the package tests"})
result("t5", {"stdout": "--- FAIL: TestGetMissingNoteIs404 (0.00s)\n"
                        "    handler_test.go:11: status = 500, want 404\n"
                        "FAIL\tnotes-api/internal/http\t0.112s\n",
              "stderr": "", "interrupted": False},
       text="Error: Exit code 1")

say("Failing for the right reason — the router wasn't wired to the new path. "
    "Fixing that through the shell.")

# A shell edit, which records its diff in bashEditDiff rather than structuredPatch.
tool("t6", "Bash", {"command": "python3 - <<'PY'\n"
                    "p='internal/http/router.go'\n"
                    "s=open(p).read()\n"
                    "s=s.replace('r.Get(\"/notes\", s.listNotes)',\n"
                    "            's.Get(\"/notes\", s.listNotes)\\n\\tr.Get(\"/notes/{id}\", s.getNote)')\n"
                    "open(p,'w').write(s)\nPY",
                    "description": "Register the single-note route"})
result("t6", {"stdout": "", "stderr": "", "interrupted": False,
              "bashEditDiff": {"moreFiles": 0, "shared": True, "files": [
                  {"filePath": f"{CWD}/internal/http/router.go", "hunks": [
                      hunk(22, 4, 22, 5, [
                          " func (s *Server) routes(r chi.Router) {",
                          " \tr.Get(\"/notes\", s.listNotes)",
                          "+\tr.Get(\"/notes/{id}\", s.getNote)",
                          " \tr.Post(\"/notes\", s.createNote)",
                      ])]}]}})

tool("t7", "Bash", {"command": "go test ./internal/... 2>&1 | tail -5",
                    "description": "Re-run the tests"})
result("t7", {"stdout": "ok  \tnotes-api/internal/http\t0.139s\n"
                        "ok  \tnotes-api/internal/store\t0.084s\n",
              "stderr": "", "interrupted": False})

say("Green. I'll hand the store coverage to an agent while I tidy the error text.")

tool("t8", "Agent", {"subagent_type": "general-purpose",
                     "description": "Add store package tests",
                     "prompt": "Add table-driven tests for internal/store covering Get, "
                               "Put and the ErrNotFound path."})
result("t8", {"stdout": "Async agent launched successfully.\n"
                        f"agentId: {AGENT} (internal ID)\n", "stderr": "", "interrupted": False},
       text=f"Async agent launched successfully.\nagentId: {AGENT} (internal ID)")

tool("t9", "Edit", {"file_path": f"{CWD}/internal/store/store.go"})
result("t9", {"filePath": f"{CWD}/internal/store/store.go", "structuredPatch": [
    hunk(15, 5, 15, 5, [
        " // ErrNotFound is returned when no note exists for the given id.",
        "-var ErrNotFound = errors.New(\"note not found\")",
        "+var ErrNotFound = errors.New(\"store: note not found\")",
        " ",
        " type Store struct {",
    ])]})

say("Done. The handler now maps `store.ErrNotFound` to 404, the route is "
    "registered, and both packages pass.")

# ----------------------------------------------------------------- the subagent
agent_lines = []


def a_entry(obj):
    obj.update({"isSidechain": True, "agentId": AGENT, "attributionAgent": "general-purpose",
                "sessionId": SESSION, "cwd": CWD, "timestamp": ts()})
    agent_lines.append(obj)


a_entry({"type": "user", "uuid": "s1",
         "message": {"role": "user", "content": "Add table-driven tests for internal/store "
                                                "covering Get, Put and the ErrNotFound path."}})
a_entry({"type": "assistant", "uuid": "s2",
         "message": {"id": "msg_s2", "role": "assistant", "model": "claude-haiku-4-5",
                     "usage": USAGE,
                     "content": [{"type": "text", "text": "Reading the store first."}]}})
a_entry({"type": "assistant", "uuid": "s3",
         "message": {"id": "msg_s3", "role": "assistant", "model": "claude-haiku-4-5",
                     "usage": USAGE,
                     "content": [{"type": "tool_use", "id": "s_t1", "name": "Read",
                                  "input": {"file_path": f"{CWD}/internal/store/store.go"}}]}})
a_entry({"type": "user", "uuid": "s4",
         "toolUseResult": {"file": {"filePath": f"{CWD}/internal/store/store.go",
                                    "numLines": 62, "startLine": 1, "totalLines": 62}},
         "message": {"role": "user", "content": [
             {"type": "tool_result", "tool_use_id": "s_t1", "content": "ok"}]}})
a_entry({"type": "assistant", "uuid": "s5",
         "message": {"id": "msg_s5", "role": "assistant", "model": "claude-haiku-4-5",
                     "usage": USAGE,
                     "content": [{"type": "tool_use", "id": "s_t2", "name": "Write",
                                  "input": {"file_path": f"{CWD}/internal/store/store_test.go",
                                            "content": "package store\n\nimport (\n\t\"context\"\n"
                                                       "\t\"errors\"\n\t\"testing\"\n)\n\n"
                                                       "func TestGetMissing(t *testing.T) {\n"
                                                       "\ts := New()\n"
                                                       "\t_, err := s.Get(context.Background(), \"nope\")\n"
                                                       "\tif !errors.Is(err, ErrNotFound) {\n"
                                                       "\t\tt.Fatalf(\"err = %v, want ErrNotFound\", err)\n\t}\n}\n"}}]}})
a_entry({"type": "user", "uuid": "s6",
         "toolUseResult": {"filePath": f"{CWD}/internal/store/store_test.go",
                           "structuredPatch": []},
         "message": {"role": "user", "content": [
             {"type": "tool_result", "tool_use_id": "s_t2", "content": "ok"}]}})
a_entry({"type": "assistant", "uuid": "s7",
         "message": {"id": "msg_s7", "role": "assistant", "model": "claude-haiku-4-5",
                     "usage": USAGE,
                     "content": [{"type": "text",
                                  "text": "Added store_test.go covering the ErrNotFound path."}]}})

# ------------------------------------------------- a few more, to fill the list
#
# A one-row session list says nothing about what the tool is for. These are
# short and invented, across a couple of projects, with ages spread out.
EXTRAS = [
    ("notestore", CWD, "2f8b6c14-9ae3-4d27-b05f-61c7d9a3e8f2", 5,
     "Add a /healthz endpoint that reports the store connection."),
    ("notestore", CWD, "9d41e7b2-6c08-4a15-83fe-2b7a0f9c5d31", 31,
     "Swap the in-memory store for SQLite behind the same interface."),
    ("webhooks", "/tmp/devdemo/webhooks", "4a92c5d7-1f36-4b80-a7e2-8c0d3f6b9a14", 50,
     "Retry failed webhook deliveries with exponential backoff."),
    ("webhooks", "/tmp/devdemo/webhooks", "b8e03f14-7d25-4c69-9a31-5f8c2e7d0b46", 125,
     "Why is the delivery worker holding connections open after a 504?"),
    ("dotfiles", "/tmp/devdemo/dotfiles", "61c7a284-3e9b-42d7-85f0-9a4e1c6b8d53", 1510,
     "Tidy up the zsh prompt so it stops wrapping on narrow terminals."),
]

extra_sessions = []
for name, cwd, sid, mins_ago, prompt in EXTRAS:
    base = NOW - timedelta(minutes=mins_ago)
    rows = [
        {"type": "user", "uuid": "x1", "sessionId": sid, "cwd": cwd,
         "timestamp": base.isoformat().replace("+00:00", "Z"),
         "message": {"role": "user", "content": prompt}},
        {"type": "assistant", "uuid": "x2", "sessionId": sid, "cwd": cwd,
         "timestamp": (base + timedelta(seconds=14)).isoformat().replace("+00:00", "Z"),
         "message": {"id": "mx2", "role": "assistant", "model": "claude-opus-4-5",
                     "usage": USAGE,
                     "content": [{"type": "text", "text": "Taking a look."}]}},
        {"type": "assistant", "uuid": "x3", "sessionId": sid, "cwd": cwd,
         "timestamp": (base + timedelta(seconds=40)).isoformat().replace("+00:00", "Z"),
         "message": {"id": "mx3", "role": "assistant", "model": "claude-opus-4-5",
                     "usage": USAGE,
                     "content": [{"type": "tool_use", "id": "xt1", "name": "Bash",
                                  "input": {"command": "go test ./...",
                                            "description": "Run the tests"}}]}},
        {"type": "user", "uuid": "x4", "sessionId": sid, "cwd": cwd,
         "timestamp": (base + timedelta(seconds=52)).isoformat().replace("+00:00", "Z"),
         "toolUseResult": {"stdout": "ok\tall packages\n", "stderr": "", "interrupted": False},
         "message": {"role": "user", "content": [
             {"type": "tool_result", "tool_use_id": "xt1", "content": "ok"}]}},
    ]
    extra_sessions.append((name, cwd, sid, rows))

# ----------------------------------------------------------------- write it out
if os.path.isdir(HOME):
    shutil.rmtree(HOME)
sub = os.path.join(PROJECT_DIR, SESSION, "subagents")
os.makedirs(sub, exist_ok=True)


def dump(path, rows):
    with open(path, "w") as f:
        for r in rows:
            f.write(json.dumps(r) + "\n")


dump(os.path.join(PROJECT_DIR, f"{SESSION}.jsonl"), lines)
for name, cwd, sid, rows in extra_sessions:
    d = os.path.join(HOME, ".claude", "projects", cwd.replace("/", "-"))
    os.makedirs(d, exist_ok=True)
    dump(os.path.join(d, f"{sid}.jsonl"), rows)
    os.makedirs(cwd, exist_ok=True)
    open(os.path.join(cwd, "README.md"), "w").write("# demo\n")
dump(os.path.join(sub, f"agent-{AGENT}.jsonl"), agent_lines)
with open(os.path.join(sub, f"agent-{AGENT}.meta.json"), "w") as f:
    json.dump({"agentType": "general-purpose", "description": "Add store package tests",
               "toolUseId": "t8", "spawnDepth": 1}, f)

# A real project directory, so the tree has something to show.
for rel in ["go.mod", "README.md", "cmd/server/main.go", "internal/http/handler.go",
            "internal/http/handler_test.go", "internal/http/router.go",
            "internal/store/store.go", "internal/store/store_test.go",
            "internal/store/memory.go", "docs/api.md"]:
    p = os.path.join(CWD, rel)
    os.makedirs(os.path.dirname(p), exist_ok=True)
    open(p, "w").write("// demo\n")

print("wrote", PROJECT_DIR)
print("session lines:", len(lines), "| agent lines:", len(agent_lines))
