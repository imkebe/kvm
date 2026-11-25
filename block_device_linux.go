//go:build linux

package kvm

import (
	"github.com/pojntfx/go-nbd/pkg/client"
)

func (d *NBDDevice) runClientConn() {
	// The block size becomes the logical sector size of /dev/nbd0, and
	// f_mass_storage hands that sector size to the target. Disk images (a
	// folder from the browser, a USB stick image) are laid out in 512-byte
	// sectors: with 4 KiB the target looks for the partition 8 times too far
	// in and sees no file system. CD-ROM mode uses 2048 regardless.
	err := client.Connect(d.clientConn, d.dev, &client.Options{
		ExportName: "jetkvm",
		BlockSize:  nbdBlockSize,
	})
	d.l.Info().Err(err).Msg("nbd client exited")
}

func (d *NBDDevice) Close() {
	if d.dev != nil {
		err := client.Disconnect(d.dev)
		if err != nil {
			d.l.Warn().Err(err).Msg("error disconnecting nbd client")
		}
		_ = d.dev.Close()
	}
	if d.listener != nil {
		_ = d.listener.Close()
	}
	if d.clientConn != nil {
		_ = d.clientConn.Close()
	}
	if d.serverConn != nil {
		_ = d.serverConn.Close()
	}
}
