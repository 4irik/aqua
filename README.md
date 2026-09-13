# aqua

**[Читать по-русски → README.ru.md](README.ru.md)**

Console dictation tool: records one take from the microphone, sends it to the
[AquaVoice API](https://aquavoice.com/docs/api), and puts the resulting text on
the clipboard and stdout.

## Requirements

- Linux with PulseAudio/PipeWire
- `parecord` (package `pulseaudio-utils`) or `ffmpeg` — audio capture
- `wl-copy` (package `wl-clipboard`) — clipboard, optional (`--no-clipboard`)
- Go toolchain — build only

## Installation

### Go

```bash
go install github.com/4irik/aqua@latest
```

### Build (requires Go 1.26+)

```bash
git clone https://github.com/4irik/aqua.git
cd aqua
go build
```

## API keys

Keys are created in the Aqua dashboard: sign in at
[aquavoice.com](https://aquavoice.com) → Account → API keys. **API billing must
be active** to run transcription.

There are two key types; which one you need depends on the mode:

| Mode | Key type in dashboard | Scope | Env var |
|---|---|---|---|
| `aqua` (default) | **Avalon transcription** | `transcription` | `AQUAVOICE_AVALON_KEY` |
| `aqua --dictate` | **Aqua data** | `write` | `AQUAVOICE_API_KEY` |

`AQUAVOICE_API_KEY` also works as a fallback for transcription mode if the key
has the right scope.

```sh
export AQUAVOICE_AVALON_KEY=...
export AQUAVOICE_API_KEY=...
```

## Usage

```sh
aqua                  # start recording, Enter or Ctrl-C to stop → transcript
aqua --dictate        # same, but text is formatted (account dictionary,
                      # replacements and instructions are applied)
aqua --language=ru    # force language (default: auto)
aqua --no-clipboard   # print to stdout only
```

The text is always printed to stdout; with `wl-copy` present it is also copied
to the clipboard. An empty take (stopped before the mic produced audio) exits
silently with code 0.

> **Note:** if your default audio source is a Bluetooth headset, it takes ~2 s
> to wake up. Takes shorter than that come out empty — either hold the take a
> bit longer or switch the default source (`pactl set-default-source`).

## Flags

| Flag | Default | Purpose |
|---|---|---|
| `--dictate` | off | Formatted dictation via `/dictations` instead of raw transcription |
| `--language` | `auto` | Language code passed to the API |
| `--model` | `avalon-v1.5` | Avalon model (transcription mode) |
| `--no-clipboard` | off | Stdout only |
| `--verbose` | off | After stopping: take duration and WAV size; while waiting for the API: a stderr spinner |

## How it works

1. `parecord` (or `ffmpeg -f pulse`) records into a temporary WAV file.
2. Stop signal (Enter/SIGINT) gracefully finishes the WAV.
3. The file goes up as `multipart/form-data` to the selected endpoint.
4. On `504 request_timeout` the job id is polled at
   `/audio/transcription-jobs/{id}/result` until the result is ready.
5. `text` from the response → stdout + clipboard.

## Development

```sh
go test ./...       # httptest-mocked unit tests
```

Inspecting the wire request:

- `scripts/dump-server.py` is a local echo server that prints every request in
  full — all headers (**including the Authorization key**, it is a debug tool)
  and each multipart field. Point `aqua` at it via the `AQUA_BASE_URL` env var:

  ```sh
  python3 scripts/dump-server.py &
  AQUA_BASE_URL=http://127.0.0.1:8799 AQUAVOICE_AVALON_KEY=x ./aqua
  ```

- Against the real API, `GODEBUG=http2debug=2 ./aqua` dumps the HTTP/2 frames:
  headers and DATA frame sizes, but not the TLS-encrypted body.

## Not included (out of scope)

Continuous mode with silence detection, push-to-talk via a hotkey daemon,
text post-processing, config file, take history.

Design details: [SPEC.md](SPEC.md) (in Russian).
