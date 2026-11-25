//go:build linux && cgo

package audio

/*
#cgo LDFLAGS: -ldl

#include <dlfcn.h>
#include <errno.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef struct _snd_pcm snd_pcm_t;
typedef struct _snd_pcm_hw_params snd_pcm_hw_params_t;
typedef struct _snd_pcm_sw_params snd_pcm_sw_params_t;
typedef long snd_pcm_sframes_t;
typedef unsigned long snd_pcm_uframes_t;

enum {
	JKP_SND_PCM_STREAM_PLAYBACK = 0,
	JKP_SND_PCM_ACCESS_RW_INTERLEAVED = 3,
	JKP_SND_PCM_FORMAT_S16_LE = 2,
	JKP_SND_PCM_NONBLOCK = 1,
};

typedef struct {
	void *lib;
	int (*pcm_open)(snd_pcm_t **pcm, const char *name, int stream, int mode);
	int (*pcm_close)(snd_pcm_t *pcm);
	size_t (*hw_params_sizeof)(void);
	size_t (*sw_params_sizeof)(void);
	int (*hw_params_any)(snd_pcm_t *pcm, snd_pcm_hw_params_t *params);
	int (*hw_params_set_access)(snd_pcm_t *pcm, snd_pcm_hw_params_t *params, int access);
	int (*hw_params_set_format)(snd_pcm_t *pcm, snd_pcm_hw_params_t *params, int format);
	int (*hw_params_set_channels)(snd_pcm_t *pcm, snd_pcm_hw_params_t *params, unsigned int channels);
	int (*hw_params_set_rate_near)(snd_pcm_t *pcm, snd_pcm_hw_params_t *params, unsigned int *rate, int *dir);
	int (*hw_params_set_period_size_near)(snd_pcm_t *pcm, snd_pcm_hw_params_t *params, snd_pcm_uframes_t *val, int *dir);
	int (*hw_params_set_buffer_size_near)(snd_pcm_t *pcm, snd_pcm_hw_params_t *params, snd_pcm_uframes_t *val);
	int (*hw_params)(snd_pcm_t *pcm, snd_pcm_hw_params_t *params);
	int (*sw_params_current)(snd_pcm_t *pcm, snd_pcm_sw_params_t *params);
	int (*sw_params_set_start_threshold)(snd_pcm_t *pcm, snd_pcm_sw_params_t *params, snd_pcm_uframes_t val);
	int (*sw_params)(snd_pcm_t *pcm, snd_pcm_sw_params_t *params);
	int (*pcm_prepare)(snd_pcm_t *pcm);
	snd_pcm_sframes_t (*pcm_writei)(snd_pcm_t *pcm, const void *buffer, snd_pcm_uframes_t size);
	int (*pcm_recover)(snd_pcm_t *pcm, int err, int silent);
	const char *(*strerror)(int errnum);
} jkp_alsa_api;

typedef struct {
	snd_pcm_t *pcm;
} jkp_alsa_playback;

static jkp_alsa_api jkp_alsa;
static int jkp_alsa_loaded = 0;

static int jkp_load_sym(void **target, const char *name) {
	*target = dlsym(jkp_alsa.lib, name);
	return *target == NULL ? -1 : 0;
}

static int jkp_alsa_load(char *errbuf, int errbuf_len) {
	if (jkp_alsa_loaded) {
		return 0;
	}
	jkp_alsa.lib = dlopen("libasound.so.2", RTLD_NOW | RTLD_LOCAL);
	if (jkp_alsa.lib == NULL) {
		snprintf(errbuf, errbuf_len, "dlopen libasound.so.2 failed: %s", dlerror());
		return -1;
	}
	int err = 0;
	err |= jkp_load_sym((void **)&jkp_alsa.pcm_open, "snd_pcm_open");
	err |= jkp_load_sym((void **)&jkp_alsa.pcm_close, "snd_pcm_close");
	err |= jkp_load_sym((void **)&jkp_alsa.hw_params_sizeof, "snd_pcm_hw_params_sizeof");
	err |= jkp_load_sym((void **)&jkp_alsa.sw_params_sizeof, "snd_pcm_sw_params_sizeof");
	err |= jkp_load_sym((void **)&jkp_alsa.hw_params_any, "snd_pcm_hw_params_any");
	err |= jkp_load_sym((void **)&jkp_alsa.hw_params_set_access, "snd_pcm_hw_params_set_access");
	err |= jkp_load_sym((void **)&jkp_alsa.hw_params_set_format, "snd_pcm_hw_params_set_format");
	err |= jkp_load_sym((void **)&jkp_alsa.hw_params_set_channels, "snd_pcm_hw_params_set_channels");
	err |= jkp_load_sym((void **)&jkp_alsa.hw_params_set_rate_near, "snd_pcm_hw_params_set_rate_near");
	err |= jkp_load_sym((void **)&jkp_alsa.hw_params_set_period_size_near, "snd_pcm_hw_params_set_period_size_near");
	err |= jkp_load_sym((void **)&jkp_alsa.hw_params_set_buffer_size_near, "snd_pcm_hw_params_set_buffer_size_near");
	err |= jkp_load_sym((void **)&jkp_alsa.hw_params, "snd_pcm_hw_params");
	err |= jkp_load_sym((void **)&jkp_alsa.sw_params_current, "snd_pcm_sw_params_current");
	err |= jkp_load_sym((void **)&jkp_alsa.sw_params_set_start_threshold, "snd_pcm_sw_params_set_start_threshold");
	err |= jkp_load_sym((void **)&jkp_alsa.sw_params, "snd_pcm_sw_params");
	err |= jkp_load_sym((void **)&jkp_alsa.pcm_prepare, "snd_pcm_prepare");
	err |= jkp_load_sym((void **)&jkp_alsa.pcm_writei, "snd_pcm_writei");
	err |= jkp_load_sym((void **)&jkp_alsa.pcm_recover, "snd_pcm_recover");
	err |= jkp_load_sym((void **)&jkp_alsa.strerror, "snd_strerror");
	if (err != 0) {
		snprintf(errbuf, errbuf_len, "failed to load required ALSA symbol");
		return -1;
	}
	jkp_alsa_loaded = 1;
	return 0;
}

static void jkp_set_error(char *errbuf, int errbuf_len, const char *op, int err) {
	if (jkp_alsa.strerror) {
		snprintf(errbuf, errbuf_len, "%s: %s", op, jkp_alsa.strerror(err));
	} else {
		snprintf(errbuf, errbuf_len, "%s: %d", op, err);
	}
}

static int jkp_configure(snd_pcm_t *pcm, unsigned int rate, unsigned int channels, unsigned long period_frames, unsigned int periods, char *errbuf, int errbuf_len) {
	int err = 0;
	snd_pcm_hw_params_t *hw = calloc(1, jkp_alsa.hw_params_sizeof());
	snd_pcm_sw_params_t *sw = calloc(1, jkp_alsa.sw_params_sizeof());
	if (hw == NULL || sw == NULL) {
		free(hw);
		free(sw);
		snprintf(errbuf, errbuf_len, "failed to allocate ALSA params");
		return -ENOMEM;
	}
	const char *op = "snd_pcm_hw_params_any";
	err = jkp_alsa.hw_params_any(pcm, hw);
	if (err < 0) goto fail;
	op = "snd_pcm_hw_params_set_access";
	err = jkp_alsa.hw_params_set_access(pcm, hw, JKP_SND_PCM_ACCESS_RW_INTERLEAVED);
	if (err < 0) goto fail;
	op = "snd_pcm_hw_params_set_format";
	err = jkp_alsa.hw_params_set_format(pcm, hw, JKP_SND_PCM_FORMAT_S16_LE);
	if (err < 0) goto fail;
	op = "snd_pcm_hw_params_set_channels";
	err = jkp_alsa.hw_params_set_channels(pcm, hw, channels);
	if (err < 0) goto fail;
	op = "snd_pcm_hw_params_set_rate_near";
	err = jkp_alsa.hw_params_set_rate_near(pcm, hw, &rate, NULL);
	if (err < 0) goto fail;
	snd_pcm_uframes_t period = period_frames;
	op = "snd_pcm_hw_params_set_period_size_near";
	err = jkp_alsa.hw_params_set_period_size_near(pcm, hw, &period, NULL);
	if (err < 0) goto fail;
	snd_pcm_uframes_t buffer = period * periods;
	op = "snd_pcm_hw_params_set_buffer_size_near";
	err = jkp_alsa.hw_params_set_buffer_size_near(pcm, hw, &buffer);
	if (err < 0) goto fail;
	op = "snd_pcm_hw_params";
	err = jkp_alsa.hw_params(pcm, hw);
	if (err < 0) goto fail;
	op = "snd_pcm_sw_params_current";
	err = jkp_alsa.sw_params_current(pcm, sw);
	if (err < 0) goto fail;
	op = "snd_pcm_sw_params_set_start_threshold";
	err = jkp_alsa.sw_params_set_start_threshold(pcm, sw, period);
	if (err < 0) goto fail;
	op = "snd_pcm_sw_params";
	err = jkp_alsa.sw_params(pcm, sw);
	if (err < 0) goto fail;
	op = "snd_pcm_prepare";
	err = jkp_alsa.pcm_prepare(pcm);
	if (err < 0) goto fail;
	free(hw);
	free(sw);
	return 0;
fail:
	jkp_set_error(errbuf, errbuf_len, op, err);
	free(hw);
	free(sw);
	return err;
}

static jkp_alsa_playback *jkp_alsa_playback_open(const char *device, unsigned int rate, unsigned int channels, unsigned long period_frames, unsigned int periods, char *errbuf, int errbuf_len) {
	if (jkp_alsa_load(errbuf, errbuf_len) != 0) {
		return NULL;
	}
	jkp_alsa_playback *pb = calloc(1, sizeof(jkp_alsa_playback));
	if (pb == NULL) {
		snprintf(errbuf, errbuf_len, "failed to allocate playback");
		return NULL;
	}
	// Non-blocking: while the host is not recording, the gadget does not drain
	// the buffer. Frames that do not fit are dropped, so latency stays bounded.
	int err = jkp_alsa.pcm_open(&pb->pcm, device, JKP_SND_PCM_STREAM_PLAYBACK, JKP_SND_PCM_NONBLOCK);
	if (err < 0) {
		jkp_set_error(errbuf, errbuf_len, "snd_pcm_open", err);
		free(pb);
		return NULL;
	}
	err = jkp_configure(pb->pcm, rate, channels, period_frames, periods, errbuf, errbuf_len);
	if (err < 0) {
		jkp_alsa.pcm_close(pb->pcm);
		free(pb);
		return NULL;
	}
	return pb;
}

// Returns the number of frames accepted (0 when the buffer is full), or a
// negative errno when the device is gone.
static int jkp_alsa_playback_write(jkp_alsa_playback *pb, const void *buffer, unsigned long frames) {
	snd_pcm_sframes_t rc = jkp_alsa.pcm_writei(pb->pcm, buffer, frames);
	if (rc >= 0) {
		return (int)rc;
	}
	if (rc == -EAGAIN) {
		return 0;
	}
	// Underrun (-EPIPE) and suspend: re-prepare and drop this chunk.
	int recovered = jkp_alsa.pcm_recover(pb->pcm, (int)rc, 1);
	if (recovered >= 0) {
		return 0;
	}
	return (int)rc;
}

static void jkp_alsa_playback_close(jkp_alsa_playback *pb) {
	if (pb == NULL) {
		return;
	}
	if (pb->pcm != NULL) {
		jkp_alsa.pcm_close(pb->pcm);
	}
	free(pb);
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// ALSAPlayback writes PCM to the UAC1 gadget's playback stream, which the
// host records as a USB microphone.
type ALSAPlayback struct {
	handle unsafe.Pointer
}

// OpenALSAPlayback opens device for signed 16-bit interleaved playback at
// PlaybackSampleRate with PlaybackChannels channels.
func OpenALSAPlayback(device string) (*ALSAPlayback, error) {
	cDevice := C.CString(device)
	defer C.free(unsafe.Pointer(cDevice))

	errBuf := make([]byte, 256)
	handle := C.jkp_alsa_playback_open(
		cDevice,
		C.uint(PlaybackSampleRate),
		C.uint(PlaybackChannels),
		C.ulong(PlaybackPeriodFrames),
		C.uint(PlaybackPeriods),
		(*C.char)(unsafe.Pointer(&errBuf[0])),
		C.int(len(errBuf)),
	)
	if handle == nil {
		return nil, fmt.Errorf("%s", C.GoString((*C.char)(unsafe.Pointer(&errBuf[0]))))
	}
	return &ALSAPlayback{handle: unsafe.Pointer(handle)}, nil
}

// WriteFrames writes interleaved stereo frames (two samples per frame). It
// returns how many frames the device accepted; frames that do not fit are
// dropped by the caller.
func (p *ALSAPlayback) WriteFrames(pcm []int16) (int, error) {
	if p.handle == nil {
		return 0, ErrPlaybackClosed
	}
	frames := len(pcm) / PlaybackChannels
	if frames == 0 {
		return 0, nil
	}
	rc := C.jkp_alsa_playback_write(
		(*C.jkp_alsa_playback)(p.handle),
		unsafe.Pointer(&pcm[0]),
		C.ulong(frames),
	)
	if rc < 0 {
		return 0, fmt.Errorf("snd_pcm_writei: %d", int(rc))
	}
	return int(rc), nil
}

func (p *ALSAPlayback) Close() error {
	if p.handle != nil {
		C.jkp_alsa_playback_close((*C.jkp_alsa_playback)(p.handle))
		p.handle = nil
	}
	return nil
}
