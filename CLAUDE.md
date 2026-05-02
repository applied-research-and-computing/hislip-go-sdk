# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Reference Implementation

The canonical reference is `../hislip-python-sdk`. Read it when designing Go equivalents — protocol constants, SCPI normalization rules, test patterns, and example instruments all port directly.

## Commands

```bash
go build ./...                              # build library + all examples
go test ./...                              # run all tests
go test -run TestEngine_IDNQuery           # run a single test by name
go test -run TestHiSLIP                    # run all protocol tests
go vet ./...                               # static analysis
gofmt -w .                                # format all files
go run ./examples/multimeter/main.go      # run an example
```

## Package Structure

Everything is in one flat package (`package hislip`) at the module root — no sub-packages. Import path: `github.com/arnc-carbon/hislip-go-sdk`.

```
command.go           # SCPICommand — parsed command passed to handlers
engine.go            # CommandEngine — SCPI dispatch + IEEE 488.2 + property store
ports.go             # Signal, InputPort, OutputPort — inter-instrument wiring
protocol.go          # HiSLIP wire format, constants, session state, lock manager
hislip_protocol.go   # HiSLIPProtocol — dual-channel TCP server (IVI-6.1)
instrument.go        # Instrument interface + InstrumentBase embed pattern
server.go            # HiSLIPServer — direct-use decorator-style API
examples/
  hello_world/       # Minimal instrument
  multimeter/        # VirtualDMM — SCPI configure/measure
  signal_generator/  # VirtualSG — waveform output port
  oscilloscope/      # VirtualOSC — 4-channel, waveform data
  power_supply/      # VirtualPSU — multi-channel, channel select
  bench/             # Wired demo: SG OUTPUT → OSC CH1
```

## Core Abstractions

### CommandEngine (`engine.go`)

String-in / optional string-out SCPI processor. Thread-safe with `sync.Mutex` (state) and `sync.RWMutex` (handler list).

- `RegisterHandler(prefix, handler)` — case-insensitive, longest prefix wins
- `ProcessCommand(command)` — splits on `;`, dispatches each part
- `AddResetHook(fn)` — called after `*RST` resets engine state; hooks run **outside** the lock, so they can safely call `SetProperty`
- `SetProperty` / `GetProperty` — key/value store, uppercase keys
- `GenerateSRQ()` — sets bit 6 of STB, fires `OnSRQ` callback outside the lock

Handler type: `func(*SCPICommand) (string, bool)` — return `(response, true)` for queries, `("", false)` for writes.

SCPI normalization (matches Python exactly): strip leading colons, collapse `::` → `:`, uppercase before matching.

### HiSLIPProtocol (`protocol.go` + `hislip_protocol.go`)

IVI-6.1 TCP server. Each client session has two TCP connections on the same port:

**Wire format** — 16-byte header + payload (big-endian):
```
"HS" (2B) | msg_type (1B) | control_code (1B) | msg_param (4B) | payload_len (8B)
```

**Initialization handshake:**
1. Sync: client sends `msgInitialize` (sub-address payload, e.g. `"hislip0"`)
2. Server replies `msgInitializeResponse`: `msg_param = (version<<16) | session_id`
3. Async: client sends `msgAsyncInitialize` (session_id in msg_param)
4. Server replies `msgAsyncInitializeResponse`: `msg_param = VendorID (0x00004342)`

Session goroutines: `syncLoop` (commands, triggers, device clear) + `asyncLoop` (lock, status, max-msg-size, SRQ).

Device clear is a 4-step handshake; see `syncLoop` / `asyncLoop` in `hislip_protocol.go`.

`safeProcessCommand` wraps `ProcessCommand` with `recover` so panicking handlers return an error string instead of crashing the session.

### InstrumentBase (`instrument.go`)

Embed pattern — struct embedding, not inheritance:

```go
type VirtualDMM struct {
    hislip.InstrumentBase
    // instrument state
}

func (d *VirtualDMM) SetupCommands() { /* register handlers */ }
func (d *VirtualDMM) OnInputChanged(port string, signal hislip.Signal) { /* react to wired signals */ }

dmm := &VirtualDMM{...}
dmm.Init(dmm, "ACME", "DMM100", "SN001", "1.0.0", true, ";", proto)
dmm.Start()
```

`Init` requires the `self` pointer so `Start()` can call the overridden `SetupCommands`. `SetupCommands` is called once on the first `Start()` call.

Wiring: `sg.Connect("OUTPUT", &osc.InstrumentBase, "CH1")` — uses `&osc.InstrumentBase` because `Connect` takes `*InstrumentBase`.

### Ports (`ports.go`)

`OutputPort.ConnectTo(input)` propagates the current signal immediately (initial state). `OutputPort.Emit(signal)` fans out to all connected inputs. `InputPort.receive` calls the `onChange` callback (set by `AddInput` to call `OnInputChanged`).

## HiSLIP Message Constants

Defined in `protocol.go` as unexported constants (`msgInitialize`, `msgDataEnd`, etc.) matching the Python values. See the const block for the full list (0–23).

## VISA Resource String

```
TCPIP0::127.0.0.1::hislip0::INSTR
```

Default port: **4880**. Sub-address `hislip0` validated on sync-channel init.

## go.mod

No external dependencies. Core uses: `encoding/binary`, `io`, `net`, `sync`, `sync/atomic`, `log`, `strconv`.
