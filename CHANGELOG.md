# Changelog

## v0.1.0 (2026-08-26)

Initial release: a Go library that drives the Xiaomi Portable Photo Printer
Pro (made by Hannto) over USB.

- `miotto`: high-level API (`Printer.Print` — context cancellation, progress
  callback, copy count)
- `protocol` / `session` / `imaging`: sans-I/O lower layers (testable without
  the device)
- `transport`: USB layer via gousb (requires libusb)
- `cmd/miotto`: the `print` command
- Verified on the actual device: full print flow, out-of-paper handling
  (pending → automatic resume once paper is loaded), open-cover detection,
  power-off detection
