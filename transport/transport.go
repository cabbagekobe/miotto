// Package transport is the USB communication with the actual device — a thin
// layer of device discovery, claiming, and bulk transfers, with no logic of
// its own (its verification is real printing on the device).
//
// It is a port of the procedure proven during the analysis: open
// VID 0x302C / PID 0x3012, claim IF#2 (HanntoUSB), and do bulk transfers on
// EP 0x06 (OUT) / 0x86 (IN). Neither set_configuration nor kernel driver
// detaching is needed (proven on macOS).
package transport

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/gousb"
)

const (
	vendorID        = 0x302C // Hannto
	productID       = 0x3012
	interfaceNumber = 2     // HanntoUSB (debug shell + JSON-RPC)
	endpointNumber  = 6     // OUT 0x06 / IN 0x86
	readBufferSize  = 16384 // headroom beyond MaxPacket (10240); the value proven during analysis
	writeTimeout    = 10 * time.Second
)

// ErrNotFound indicates the printer is not present on the USB bus.
// The device powers itself off after a few minutes and disappears from the
// USB tree.
var ErrNotFound = errors.New(
	"printer not found (the device powers off automatically after a few minutes; turn it on, reconnect USB, and retry)")

// Device is a claimed printer. It is meant to be used from a single goroutine
// and does not guard against concurrent access (the print flow is a strictly
// sequential write/read exchange).
type Device struct {
	ctx  *gousb.Context
	dev  *gousb.Device
	cfg  *gousb.Config
	intf *gousb.Interface
	out  *gousb.OutEndpoint
	in   *gousb.InEndpoint
}

// Open finds and claims the printer. Call Close when done.
func Open() (*Device, error) {
	d := &Device{ctx: gousb.NewContext()}
	var err error
	d.dev, err = d.ctx.OpenDeviceWithVIDPID(vendorID, productID)
	if err == nil && d.dev == nil {
		err = ErrNotFound
	}
	if err != nil {
		d.Close()
		return nil, err
	}
	if d.cfg, err = d.dev.Config(1); err != nil {
		d.Close()
		return nil, fmt.Errorf("getting configuration: %w", err)
	}
	if d.intf, err = d.cfg.Interface(interfaceNumber, 0); err != nil {
		d.Close()
		return nil, fmt.Errorf("claiming interface %d: %w", interfaceNumber, err)
	}
	if d.out, err = d.intf.OutEndpoint(endpointNumber); err != nil {
		d.Close()
		return nil, fmt.Errorf("opening OUT endpoint: %w", err)
	}
	if d.in, err = d.intf.InEndpoint(endpointNumber); err != nil {
		d.Close()
		return nil, fmt.Errorf("opening IN endpoint: %w", err)
	}
	return d, nil
}

// Write writes one frame via bulk OUT.
func (d *Device) Write(frame []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	_, err := d.out.WriteContext(ctx, frame)
	return err
}

// Read performs one bulk IN read. It returns an error if no data arrives
// within the timeout.
func (d *Device) Read(timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	buf := make([]byte, readBufferSize)
	n, err := d.in.ReadContext(ctx, buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

// Close releases the claim and all handles (best effort). It is safe to call
// even after a failed Open.
func (d *Device) Close() {
	if d.intf != nil {
		d.intf.Close()
	}
	if d.cfg != nil {
		_ = d.cfg.Close()
	}
	if d.dev != nil {
		_ = d.dev.Close()
	}
	if d.ctx != nil {
		_ = d.ctx.Close()
	}
}
