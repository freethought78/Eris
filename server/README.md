# eris-server

The Eris dedicated server (DESIGN.md section 3). This first build has what
testing between homes needs; text, files, voice and LiveKit come later.

```bash
go build -o bin/eris-server .
bin/eris-server -public myname.example.net      # -addr :7300, -data DIR
```

First run creates `eris-data/` next to the program (`settings.json`,
`eris.db`, `tests/`) and prints the join link and a one-time **owner code**:
the first person to join with it becomes the owner. Players need TCP 7300
(and, to watch or play a stream, the streamer's UDP 7401) reachable.

What it does now:

- **Joining:** `POST /api/join {name, password, owner_code}` gives a member
  token (only its hash is stored). A server password in `settings.json`, if
  set, is asked once.
- **Live connection:** `GET /api/ws`, first message `{"type":"auth","token":...}`.
  Presence, who's live, connection setup between two members' streaming
  helpers (`signal`), and stream test invitations. The message types are at
  the top of `hub.go`.
- **Stream test results:** `POST /api/tests` (each side posts its own summary)
  and `GET /api/tests`. Each result is also written to `eris-data/tests/` as a
  JSON file, so it can be read without the app.

Tests: `go test ./...`
