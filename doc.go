// Package miotto drives the Xiaomi Portable Photo Printer Pro (made by
// Hannto) over USB, printing 2×3-inch photos without the official app.
//
// The simplest way in is [Printer]:
//
//	p, err := miotto.Open()
//	if err != nil {
//		// powered off, not connected, etc.
//	}
//	defer p.Close()
//	err = p.Print(ctx, photoBytes) // print any JPEG/PNG
//
// Print progress (chunk transfer and job_state) is delivered via
// [WithProgress]. Running out of paper is reported as
// [session.JobStatePending]; the job resumes automatically once paper is
// loaded. [Printer.Status] reads the battery level and cover state, and
// [Printer.Shell] gives raw access to the firmware's debug shell.
//
// The lower layers are exported too, for applications that bring their own I/O:
//
//   - protocol: frame assembly and parsing (pure functions, no I/O)
//   - session: the print-flow state machine (sans-I/O)
//   - imaging: input image → 626×939 JPEG conversion (EXIF orientation applied)
//   - transport: the USB layer via gousb (requires libusb)
//
// The measured protocol record lives in docs/protocol.md in the repository.
package miotto
