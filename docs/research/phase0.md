# Phase 0: Results of the feasibility probe

## Conclusion

**A USB path exists, and it will be the primary path.** BLE analysis (iPhone + PacketLogger) is no longer needed.

## Background

1. The official specs say "Bluetooth 5.2 only; Type-C is USB 2.0", and USB was assumed to be for charging only
2. Actually looking at `ioreg -p IOUSB`, the device was recognized as one with a
   USB printer-class interface (`Hannto` / `Printer`, VID 0x302C / PID 0x3012)
3. CUPS also sees it as `usb://Hannto/MDK_NS_PO_NOMSC?serial=...`
4. Liene, Hannto's overseas brand, distributes a **desktop app for macOS**, and
   the `libht_device_usb_core.dylib` inside it contained a protocol implementation with symbols intact

## Prior work referenced

[honeymaro/pixcut](https://github.com/honeymaro/pixcut) — a cut-path generation tool for the Liene PixCut S1.
It publishes the model registry, error codes, and an overview of the transport as typed data.
The transport layer is unimplemented there, so that part was analyzed on our own (`docs/protocol.md`).

## Environment notes

- Analysis target: `LienePhoto_mac.dmg` (91MB, 2026-07-16)
  `sha256 = 221abe8e2d4c4b22ad2a2d5e99f9aa502093148810518df29864d1909f2bf6b4`
  Source: `https://us-hannto-static.s3.us-west-2.amazonaws.com/liene_photo-app-ota/pro/LienePhoto_mac.dmg`
- In the app's model registry (`ht_device_kit/assets/pluginInfo.json`, version 2.4.212.02),
  this device (dye-sublimation 2x3) is **not registered**. The registered `hannto.printer.kiwi` (Portable Photo Printer) is
  a different ZINK-paper model. The official app is therefore unlikely to work as-is.
