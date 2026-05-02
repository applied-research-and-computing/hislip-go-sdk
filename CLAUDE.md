# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Reference Implementation

The canonical reference is `../hislip-python-sdk`. Read it when designing Go equivalents — architecture, protocol constants, SCPI normalization rules, test patterns, and example instruments all port directly.

## Commands

```bash
go build ./...                          # build all packages
go test ./...                           # run all tests
go test ./... -run TestName             # run a single test by name
go test ./internal/engine/...           # test one package
go vet ./...                            # static analysis
golangci-lint run                       # lint
gofmt -w .                             # format all files
go run ./examples/multimeter/main.go   # run an example
```

## Architecture

The SDK is a Go port of `../hislip-python-sdk` with the same layered structure:

```
hislip-instruments/
  internal/
    engine/       # CommandEngine — SCPI string-in/string-out processor
    command/      # ScpiCommand — parsed command passed to handlers
    ports/        # Signal, InputPort, OutputPort — inter-instrument wiring
  protocol/       # HiSLIPProtocol — TCP server (IVI-6.1)
  instrument/     # Instrument interface + InstrumentBase struct
examples/
  hello_world/
  multimeter/
  oscilloscope/
  power_supply/
  signal_generator/
  bench/          # wired demo: signal generator → oscilloscope
profiles/         # YAML instrument profiles for Carbon platform UI
  carbon_dmm6500.yaml
  ...
go.mod
```

### CommandEngine (`internal/engine`)

String-in / string-out SCPI processor. Thread-safe with `sync.RWMutex`. Responsibilities:
- Register handlers: `engine.RegisterHandler("CONF:VOLT:DC", handlerFn)`
- Dispatch: longest-prefix match, case-insensitive after normalization
- Built-in IEEE 488.2: `*IDN?`, `*RST`, `*CLS`, `*STB?`, `*ESR?`, `*ESE`, `*SRE`, `*OPC?`
- Property store: `SetProperty(key, val)` / `GetProperty(key)`
- Reset hooks: registered callbacks invoked on `*RST`
- SRQ generation: `GenerateSRQ()` → triggers async service request to all clients

SCPI normalization (must match Python exactly):
- Strip leading colons, collapse `::` → `:`
- Uppercase before matching
- Queries end with `?`; args are comma-separated after the prefix

### HiSLIPProtocol (`protocol/`)

TCP server implementing IVI-6.1. Uses two TCP connections per client session.

**Wire format** — 16-byte header + payload:
```
"HS" (2B) | msg_type (1B) | control_code (1B) | msg_param (4B) | payload_len (8B)
```

**Dual-channel per session:**
- Sync channel (port 4880): MSG_DATA*, MSG_TRIGGER, MSG_DEVICE_CLEAR_COMPLETE
- Async channel (port 4880): MSG_ASYNC_INITIALIZE, MSG_ASYNC_STATUS_QUERY, MSG_ASYNC_LOCK, MSG_ASYNC_DEVICE_CLEAR, MSG_ASYNC_SERVICE_REQUEST

**Initialization handshake:**
1. Sync: client sends `MSG_INITIALIZE` (sub-address in payload, e.g. `"hislip0"`)
2. Server responds `MSG_INITIALIZE_RESPONSE` with `(version << 16) | session_id`
3. Async: client sends `MSG_ASYNC_INITIALIZE` (session_id in msg_param)
4. Server responds `MSG_ASYNC_INITIALIZE_RESPONSE` with `VENDOR_ID = 0x00004342` in msg_param

**Device clear** is a 4-step handshake; see Python `protocol.py` for the exact sequence.

**Session state** is per-client: message IDs, lock ownership, MAV (Message AVailable) bit. Each session runs sync and async goroutines.

### Instrument (`instrument/`)

```go
type Instrument interface {
    SetupCommands(engine *engine.CommandEngine)
    OnInputChanged(port string, signal *ports.Signal)
}
```

`InstrumentBase` composes `CommandEngine`, a slice of protocols, and input/output ports. `Start()` calls `SetupCommands()` then begins accepting connections.

### Ports (`internal/ports`)

```go
type Signal struct {
    Value    float64
    Unit     string
    Metadata map[string]any  // frequency, offset, function type, etc.
}
```

`OutputPort.Emit(signal)` broadcasts to all connected `InputPort`s, which call `OnInputChanged` on their owning instrument. Initial value propagates on `ConnectTo()`.

## HiSLIP Protocol Constants

See `MSG_*` type values in Python `protocol.py`. Critical ones: `MSG_DATA = 0x05`, `MSG_DATA_END = 0x06`, `MSG_INITIALIZE = 0x00`, `MSG_ASYNC_SERVICE_REQUEST = 0x10`.

## VISA Resource String

```
TCPIP0::127.0.0.1::hislip0::INSTR
```

Default port: **4880**. Sub-address `hislip0` is validated during initialization.

## go.mod Dependencies

Expected dependencies:
- `gopkg.in/yaml.v3` — profile deserialization

Optional build tag `mdns`:
- `github.com/grandcat/zeroconf` — for `_hislip._tcp.local.` advertisement

The core protocol and engine should use only the standard library (`net`, `sync`, `encoding/binary`, `log`).

## Profiles

YAML files in `profiles/` describe SCPI variables and commands for the Carbon platform UI. Match the structure in `../hislip-python-sdk/profiles/`. Not parsed by the SDK itself — consumed by the Carbon platform.
