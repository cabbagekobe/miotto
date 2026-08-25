# miotto

**Use the Xiaomi Portable Photo Printer Pro from a PC.**

Officially this printer can only be driven from the Xiaomi Home smartphone app,
and its spec sheet claims "Bluetooth 5.2 only" for connectivity — but
**printing over USB has been verified on the actual device.**

## Status

| Phase | State |
|---|---|
| Protocol analysis | **Done**. The full procedure is recorded in [docs/protocol.md](docs/protocol.md) |
| Printing on the device | **Working**. Output verified with the Go implementation alone (2026-08-25, `miotto print`) |
| Library (Go) | **Done**. `protocol` / `session` / `imaging` (tested) + the USB layer `transport` (gousb, verified on the device) |
| CLI | `print` only. `status` / `shell` not started |
| GUI | Not started |

miotto is built as **a Go library that other applications import for printing**.
The print logic (frame assembly, the job-flow state machine, image conversion)
is testable without the device. To print for real:

```bash
go build ./cmd/miotto && ./miotto print testdata/testchart.jpg
```

## Using it as a library

The module is named `miotto` (a play on MIoT):

```bash
go get github.com/cabbagekobe/miotto   # building needs libusb (macOS: brew install libusb)
```

The high-level API covers ordinary use:

```go
p, err := miotto.Open() // transport.ErrNotFound when absent (turn the printer on and retry)
if err != nil { /* ... */ }
defer p.Close()

// Print one JPEG/PNG. EXIF orientation, the 626×939 resize, chunk transfer,
// and job_info polling are all taken care of. Cancel ctx to abort.
err = p.Print(ctx, photoBytes, miotto.WithProgress(func(pr miotto.Progress) {
	// pr.JobState == session.JobStatePending means out of paper;
	// the job resumes automatically once paper is loaded.
}))
```

Applications that bring their own I/O (a custom transport, an async runtime,
etc.) can use the sans-I/O lower layers directly: the
`imaging.Prepare` → `session.New` → `NextRequest` / `HandleResponse` loop.
See the session package godoc and the implementation in `miotto.go`.

## Findings (summary)

- The device exposes **two printer-class USB interfaces**
  - IF#0 `Print` — the path that declares `CMD:DC_RASTER` (unused)
  - IF#2 `HanntoUSB` — **the path actually used**: a text debug shell + JSON-RPC
- No encryption, no handshake. **Plaintext JSON-RPC**
- Printing takes just 3 steps
  1. `print_job` creates a job → `job_id`
  2. Send `"cmd data EXTLEN=<len>\n" + job_id(uint32 LE) + JPEG chunk`, at most 10240 bytes per packet
  3. Watch `job_info` for `waiting → downloading → printing → finished`
- **The host sends the JPEG itself.** Color separation, dithering, and the
  4-pass ribbon control all happen in the firmware
- The effective size is 626×939 px (2×3 inch @ 313dpi). Roughly 6–8px around
  the edges are trimmed off
- Bonus: the firmware's debug shell is reachable — `apd` (bulk sensor dump),
  `bat` (battery), `bt AT+...` (AT passthrough to the Bluetooth module), and more

The story and evidence of the analysis are in
[docs/research/phase0.md](docs/research/phase0.md); protocol details are in
[docs/protocol.md](docs/protocol.md).

## Roadmap

See [docs/plan.md](docs/plan.md). In short: keep I/O and logic separated so
everything stays testable without the device; after `transport` (gousb) and
the CLI proved out on the device, next up is the GUI (local web UI → Wails).

## Target device

| | |
|---|---|
| Product | Xiaomi Portable Photo Printer Pro |
| MIoT model | `xiaomi.printer.ricotg` |
| USB | VID `0x302C` (Hannto) / PID `0x3012` |
| IEEE1284 | `MFG:Hannto;MDL:MDK_NS_PO_NOMSC;CMD:DC_RASTER;CLS:PRINTER;` |
| Firmware | `2.1.2_0039` (at analysis time) |

Hannto (上海汉图) is the OEM behind Xiaomi's printers; their overseas brand is
Liene. [honeymaro/pixcut](https://github.com/honeymaro/pixcut), a cut-path
tool for a sibling model, publishes a model registry and a transport overview
that served as the starting point for this analysis.

## Notes

- This analysis was done for private-use interoperability. No firmware
  modification and no OTA tampering was involved
- The debug shell has commands with unpredictable side effects, such as
  `producttest` / `i2c` / `otp` (battery protection settings).
  **They were never touched, and should not be**
