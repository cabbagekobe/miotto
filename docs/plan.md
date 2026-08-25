# Implementation plan

The protocol analysis is done ([protocol.md](protocol.md)). What remains is
turning the measured procedure into a **reproducible form**. The
implementation is a **Go library** (imported by other applications), with the
end goal being a GUI app that runs on macOS / Windows / Linux (web frontend +
Wails, most likely).

The pure-logic layer was first implemented in Python (pyusb + Pillow), then
ported to Go (2026-08-25) for distributability (a single binary) and the GUI
outlook. The test expectations — the byte sequences actually sent to the
device — carried over as-is.

## Current state (2026-08-25)

```
miotto/
  README.md
  go.mod / go.sum             module github.com/cabbagekobe/miotto (go get-able; the name is a play on MIoT)
  miotto.go / doc.go          high-level API (Printer.Print — ctx, progress callback, copies)
  LICENSE / CHANGELOG.md      MIT / release notes
  protocol/                   done, green (tests match the measured byte sequences)
  session/                    done, green (full flow with fake responses; error paths tested)
  imaging/                    done, green (cover resize, 90° rotation, all 8 EXIF orientations)
  transport/                  done, verified on the device (gousb: VID/PID → claim IF#2 → bulk 0x06/0x86)
  cmd/miotto/                 CLI (print only; the print loop is tested with a fake device)
  docs/                       protocol.md, research/phase0.md, plan.md (this document)
  testdata/                   test-chart images for device runs (testchart.jpg / .png, with a proven print record)
  Makefile / mise.toml /      verification & CI harness; make check is the single entry point
  lefthook.yml / .github/
```

**Printing on the actual device with the Go implementation alone works
(2026-08-25).** Next up: expanding the CLI (status / shell) and the GUI.

## Borrowed pieces, now removed

During analysis, the official app's bundled `libht_device_usb_core.dylib`
(with libusb statically linked) was driven via ctypes. Implementing
`transport` with **gousb** (a cgo wrapper for libusb) removed that dependency.
Building requires libusb (macOS: `brew install libusb`; CI:
`libusb-1.0-0-dev`).

The analysis is finished, so `/Applications/Liene Photo.app` can be
uninstalled (everything needed is in `docs/protocol.md`). The Python analysis
scripts used during the reverse engineering served their purpose and were
removed; everything they discovered is recorded in `docs/protocol.md`.

## Architecture

Keep I/O and logic separated so **tests run without the device**.

Since other applications import this as a library, the packages are public at
the repository root (no `internal/`).

```
miotto (root) high-level API. Printer.Print bundles the imaging → session →
             transport print loop, the cover preflight, and pending progress
             notifications (ctx cancellation; tested with a fake)
protocol/    frame assembly and parsing; pure functions only, no I/O
session/     the print-flow state machine; returns "the next bytes to send", no I/O
transport/   gousb wrapper; a thin layer of discovery, claiming, and bulk transfers
imaging/     input image → 626×939 JPEG
cmd/         CLI (print is a thin wrapper over the root API; status / shell not started)
```

- `protocol` — `FrameJSON` / `FrameData` / `Chunks` / `ParseJSONResponse` /
  `IsDataAck` / `ErrorOf`. Knowledge of `"cmd json\n"` and
  `"cmd data EXTLEN=<len>\n"` is confined here. The JSON key order proven on
  the device is reproduced via struct field order (maps are unusable)
- `session` — the print_job → chunk transfer → job_info polling transitions.
  Knows nothing about USB, so the whole flow is testable with fake responses.
  Failures come back as errors (RPC errors as `RPCError`)
- `transport` — discovery, claiming, bulk transfers. The only package that
  depends on gousb (cgo)
- Dependencies: only `golang.org/x/image/draw` (cover resize). The 8-way
  orientation transform and the EXIF Orientation parser are implemented
  in-house (dropping the unmaintained `disintegration/imaging` / `goexif`;
  the parser is fuzz-tested)

## TODO

1. ~~**`protocol` (Red→Green)** — pass the tests for the measured byte sequences~~
   **Done (2026-08-25, ported to Go)**
2. ~~**`session` (Red→Green)** — drive `waiting → downloading → printing →
   finished` with fake responses; pin the handling of error responses
   (`-8201 json parse error`, `-8001 Job ID Ivalid`) in tests~~
   **Done (2026-08-25, ported to Go)**. Sans-I/O `PrintSession`
   (`NextRequest` / `HandleResponse`); never sleeps (the polling interval is
   the caller's responsibility). Response-stream robustness policy: while
   polling job_info (idempotent), ignore unknown events and mismatched-id
   responses and continue; print_job and chunk transfer (whose resends are
   not idempotent) stay fail-fast
3. ~~**`transport`** — open VID `0x302C` / PID `0x3012` and claim IF#2;
   port the procedure proven on macOS to gousb~~
   **Done (2026-08-25)**. A thin Open / Write / Read(timeout) / Close layer.
   When the device is absent it returns `ErrNotFound`, prompting a power cycle
4. ~~**`imaging` (Red→Green)** — cover-resize any image to 626×939 and encode
   as JPEG. Pin the crop position for mismatched aspect ratios and the 90°
   rotation of landscape input in tests. The outer 6–8px get trimmed, so fill
   the full area assuming borderless printing~~
   **Done (2026-08-25, ported to Go)**. EXIF orientation (smartphone photos,
   all 8 ways) is applied too
5. **`cmd/miotto`** — `print` / `status` / `shell` (interactive access to the
   debug shell). **`print` is done (2026-08-25)**; the print loop is tested
   with a fake device. status / shell not started
6. ~~**Acceptance test on the device** — print one sheet with
   `miotto print testdata/testchart.jpg`~~
   **Done (2026-08-25)**. 105,306 bytes transferred in 11 chunks, reaching
   `downloading → printing → finished` with one sheet printed
7. **GUI** — first a local web UI (Go's net/http + plain HTML/CSS/JS), then
   wrap it with Wails for distribution. Accessibility per `~/.claude/A11Y.md`
   (body text ≥ 16px, 7:1 contrast, `<button>`/`<a>` for interactions, keep
   focus rings, show progress and results as running text, no auto-dismissing
   toasts). Start **after** the CLI is proven on the device

## Verification

- `make check` (gofmt / golangci-lint / go vet / go test -race;
  protocol / session / imaging need no device)
- `make audit` (govulncheck)
- Device smoke test: `go test -tags hardware ./transport/` (needs the printer
  powered on; consumes no paper)
- Device: print one sheet via the CLI and confirm `job_state` reaches `finished`

## Open questions

### Whether claiming works on Windows is unverified

`usbprint.sys` binds to printer-class interfaces, so libusb may be unable to
claim IF#2. Whether switching to WinUSB via Zadig is needed requires testing
on a Windows machine. **Solidify macOS first.**

Linux should work after detaching `usblp` (unverified).

### The printer powers itself off after a few minutes

Once it powers off it also disappears from the USB tree. The CLI must turn
"device not found" into a clear message about power-cycling. There should be
a method to extend `auto_off_interval`, but its exact name and params are not
yet determined.

### The params format of `cancel_job` is unknown

Both `[id]` and `{"job_id":id}` return `-8001 Job ID Ivalid`. However, a job
that receives no data aborts automatically after a while without consuming
paper, so the practical impact is small for now.

### The `get_prop` vocabulary is unknown

`get_prop` itself exists and returns one reply per params element, but every
prop name tried returns an empty object `{}` (hyphenated, underscored, and
MIoT siid/piid forms were all attempted). Battery and paper levels are wanted
for the GUI, so before starting the GUI, evaluate whether the debug shell's
`apd` / `bat` (verified working) can substitute.

Measured results for the `apd` substitute (2026-08-25): **an open cover is
detectable via `cover=0`** (implemented as the CLI's preflight check).
**Paper presence is not statically detectable** (sensor values do not change).
Running out of paper is detected during printing as `job_state: pending`
(a waiting state that resumes automatically; see protocol.md).

### Uninvestigated shell commands

`cdb`, `bd` (`display_all` returns OK but its output goes to UART), and the
subcommands of `ui` `sensor` `adpt` `log`. `log start` is confirmed to stream
internal logs over USB; used well, it might expose **the traffic Xiaomi Home
sends over Bluetooth**, which makes it a promising route to learn the
`get_prop` vocabulary.
