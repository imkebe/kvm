//go:build linux && cgo

#include "output.h"
#include <errno.h>
#include <fcntl.h>
#include <linux/videodev2.h>
#include <linux/usb/g_uvc.h>
#include <linux/usb/video.h>
#include <poll.h>
#include <pthread.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/mman.h>
#include <time.h>
#include <unistd.h>
#include <endian.h>

#define BUFFER_COUNT 3
#define FRAME_QUEUE_COUNT 8
// MJPEG frame sizes, in the order of their frame descriptors (bFrameIndex
// starts at 1), and the frame intervals each one offers. They must match the
// gadget's descriptors (internal/usbgadget/video.go).
struct uvc_mode { unsigned width, height; };
// In the order of the gadget's frame descriptors: a frame's index is its
// place in this table plus 1.
static struct uvc_mode mjpeg_modes[UVC_OUTPUT_MODES] = {{640,480},{1280,720},{640,360},{1920,1080},{960,540},{800,600},{1024,768},{1600,900}};
static unsigned mjpeg_mode_count = 4;
// The frame intervals (100 ns units) the gadget offers; every frame size
// offers the same ones.
static unsigned mjpeg_intervals[UVC_OUTPUT_INTERVALS] = {333333, 666666};
static unsigned mjpeg_interval_count = 2;
static unsigned interval_shortest(void) {
    unsigned v=mjpeg_intervals[0];
    for (unsigned i=1;i<mjpeg_interval_count;i++) if (mjpeg_intervals[i]<v) v=mjpeg_intervals[i];
    return v;
}
static unsigned interval_longest(void) {
    unsigned v=mjpeg_intervals[0];
    for (unsigned i=1;i<mjpeg_interval_count;i++) if (mjpeg_intervals[i]>v) v=mjpeg_intervals[i];
    return v;
}
// interval_nearest is the offered interval closest to the host's; 0 (the
// host has no wish) gets the shortest.
static unsigned interval_nearest(unsigned want) {
    if (!want) return interval_shortest();
    unsigned best=mjpeg_intervals[0];
    for (unsigned i=1;i<mjpeg_interval_count;i++) {
        unsigned a=mjpeg_intervals[i];
        unsigned da=a>want ? a-want : want-a, db=best>want ? best-want : want-best;
        if (da<db) best=a;
    }
    return best;
}
struct encoded_frame { uint8_t *data; size_t size; };
struct output_buffer { void *data; size_t size; };
struct uvc_output {
    int fd, stopped, streaming, error, pending_control;
    unsigned width, height, fps;
    int h264, needs_keyframe;
    unsigned long long dropped;
    struct encoded_frame pending[FRAME_QUEUE_COUNT];
    unsigned pending_head, pending_count;
    int queued[BUFFER_COUNT];
    unsigned control_interface, streaming_interface;
    unsigned long long frames;
    uint64_t next_frame;
    pthread_t thread;
    pthread_mutex_t lock;
    uint8_t *frame;
    size_t frame_size, capacity;
    struct output_buffer buffers[BUFFER_COUNT];
    unsigned buffer_count;
    struct uvc_streaming_control probe, commit;
    // MJPEG: the frame sent until the browser sends one of the committed size.
    struct encoded_frame blank[UVC_OUTPUT_MODES];
};

static void clear_pending(struct uvc_output *d) {
    for (unsigned i=0; i<FRAME_QUEUE_COUNT; i++) {
        free(d->pending[i].data); d->pending[i]=(struct encoded_frame){0};
    }
    d->pending_head=0; d->pending_count=0; d->needs_keyframe=1;
}
static void stream_off(struct uvc_output *d) {
    enum v4l2_buf_type type = V4L2_BUF_TYPE_VIDEO_OUTPUT;
    if (d->streaming) ioctl(d->fd, VIDIOC_STREAMOFF, &type);
    pthread_mutex_lock(&d->lock); d->streaming = 0; clear_pending(d); pthread_mutex_unlock(&d->lock);
    memset(d->queued,0,sizeof(d->queued));
    for (unsigned i=0; i<d->buffer_count; i++) {
        if (d->buffers[i].data) munmap(d->buffers[i].data, d->buffers[i].size);
        d->buffers[i].data = NULL;
    }
    d->buffer_count = 0;
    struct v4l2_requestbuffers req = {.type=type, .memory=V4L2_MEMORY_MMAP, .count=0};
    ioctl(d->fd, VIDIOC_REQBUFS, &req);
}

static uint64_t monotonic_ns(void) {
    struct timespec t; clock_gettime(CLOCK_MONOTONIC,&t);
    return (uint64_t)t.tv_sec*1000000000+t.tv_nsec;
}

static int queue(struct uvc_output *d, struct v4l2_buffer *b) {
    if (b->index >= d->buffer_count) return -1;
    pthread_mutex_lock(&d->lock);
    size_t size = d->frame_size;
    const uint8_t *data = d->frame;
    if (size > d->buffers[b->index].size) { pthread_mutex_unlock(&d->lock); errno=EMSGSIZE; return -1; }
    if (size) memcpy(d->buffers[b->index].data, data, size);
    b->bytesused=size;
    int result=ioctl(d->fd,VIDIOC_QBUF,b);
    if (result==0) {
        d->queued[b->index]=1;
    }
    pthread_mutex_unlock(&d->lock);
    return result;
}

static int stream_on(struct uvc_output *d) {
    if (d->streaming) return 0;
    struct v4l2_format fmt = {.type=V4L2_BUF_TYPE_VIDEO_OUTPUT};
    fmt.fmt.pix.width=d->width; fmt.fmt.pix.height=d->height;
    fmt.fmt.pix.pixelformat=d->h264 ? V4L2_PIX_FMT_H264 : V4L2_PIX_FMT_MJPEG; fmt.fmt.pix.field=V4L2_FIELD_NONE;
    fmt.fmt.pix.sizeimage=d->h264 ? d->capacity : d->width*d->height*2;
    if (ioctl(d->fd, VIDIOC_S_FMT, &fmt)<0) { perror("UVC VIDIOC_S_FMT"); return -1; }
    struct v4l2_requestbuffers req = {.type=fmt.type, .memory=V4L2_MEMORY_MMAP, .count=BUFFER_COUNT};
    if (ioctl(d->fd, VIDIOC_REQBUFS, &req)<0) { perror("UVC VIDIOC_REQBUFS"); return -1; }
    if (!req.count || req.count>BUFFER_COUNT) { errno=ENOBUFS; return -1; }
    for (unsigned i=0; i<req.count; i++) {
        struct v4l2_buffer b = {.type=fmt.type, .memory=V4L2_MEMORY_MMAP, .index=i};
        if (ioctl(d->fd, VIDIOC_QUERYBUF, &b)<0) goto fail;
        void *data=mmap(NULL,b.length,PROT_READ|PROT_WRITE,MAP_SHARED,d->fd,b.m.offset);
        if (data==MAP_FAILED) goto fail;
        d->buffers[i]=(struct output_buffer){data,b.length}; d->buffer_count++;
        if (!d->h264 && queue(d,&b)<0) { perror("UVC VIDIOC_QBUF"); goto fail; }
    }
    enum v4l2_buf_type type=fmt.type;
    if (ioctl(d->fd,VIDIOC_STREAMON,&type)<0) { perror("UVC VIDIOC_STREAMON"); goto fail; }
    pthread_mutex_lock(&d->lock); d->streaming=1; d->error=0; pthread_mutex_unlock(&d->lock);
    d->next_frame=monotonic_ns();
    fprintf(stderr,"UVC output started: %ux%u at %u fps\n",d->width,d->height,d->fps);
    return 0;
fail:
    { int error=errno; stream_off(d); errno=error; return -1; }
}

static void setup(struct uvc_output *d, const struct usb_ctrlrequest *r) {
    struct uvc_request_data response = {.length=-EL2HLT};
    unsigned control=le16toh(r->wValue)>>8, unit=le16toh(r->wIndex)>>8, interface=le16toh(r->wIndex)&255;
    unsigned length=le16toh(r->wLength);
    d->pending_control=0;
    if (interface==d->control_interface && unit==0 && control==UVC_VC_REQUEST_ERROR_CODE_CONTROL && r->bRequest==UVC_GET_CUR) {
        response.length=1; response.data[0]=0;
    } else if (interface==d->streaming_interface && unit==0 && (control==UVC_VS_PROBE_CONTROL || control==UVC_VS_COMMIT_CONTROL)) {
        struct uvc_streaming_control *c = control==UVC_VS_PROBE_CONTROL ? &d->probe : &d->commit;
        switch (r->bRequest) {
        case UVC_SET_CUR:
            response.length = length < sizeof(*c) ? length : sizeof(*c);
            d->pending_control=control;
            break;
        case UVC_GET_CUR: case UVC_GET_MIN: case UVC_GET_MAX: case UVC_GET_DEF: {
            struct uvc_streaming_control value=*c;
            if (!d->h264 && r->bRequest==UVC_GET_MIN) value.dwFrameInterval=htole32(interval_shortest());
            if (!d->h264 && r->bRequest==UVC_GET_MAX) value.dwFrameInterval=htole32(interval_longest());
            if (!d->h264 && r->bRequest==UVC_GET_DEF) { value.bFrameIndex=1; value.dwFrameInterval=htole32(interval_shortest()); value.dwMaxVideoFrameSize=htole32(mjpeg_modes[0].width*mjpeg_modes[0].height*2); }
            response.length = length < sizeof(value) ? length : sizeof(value);
            memcpy(response.data,&value,response.length); break;
        }
        case UVC_GET_RES:
            response.length = length < sizeof(*c) ? length : sizeof(*c); break;
        case UVC_GET_LEN:
            response.length=2; response.data[0]=sizeof(*c); break;
        case UVC_GET_INFO:
            response.length=1; response.data[0]=3; break;
        }
    }
    ioctl(d->fd,UVCIOC_SEND_RESPONSE,&response);
}

// negotiate answers the host's probe or commit of an MJPEG stream with the
// nearest frame size and interval the camera offers.
static void negotiate(struct uvc_output *d, const struct uvc_request_data *data, int commit) {
    struct uvc_streaming_control host; memset(&host,0,sizeof(host));
    size_t length = data->length < 0 ? 0 : (size_t)data->length;
    if (length > sizeof(host)) length=sizeof(host);
    memcpy(&host,data->data,length);
    unsigned frame = host.bFrameIndex>=1 && host.bFrameIndex<=mjpeg_mode_count ? host.bFrameIndex : 1;
    unsigned interval = interval_nearest(le32toh(host.dwFrameInterval));
    const struct uvc_mode *mode=&mjpeg_modes[frame-1];
    struct uvc_streaming_control c=d->commit;
    c.bFormatIndex=1; c.bFrameIndex=frame; c.dwFrameInterval=htole32(interval);
    c.dwMaxVideoFrameSize=htole32(mode->width*mode->height*2);
    d->probe=c;
    if (!commit) return;
    d->commit=c;
    pthread_mutex_lock(&d->lock);
    d->fps=10000000/interval;
    if (d->width!=mode->width || d->height!=mode->height) {
        d->width=mode->width; d->height=mode->height;
        d->frame_size=0;
        if (d->blank[frame-1].size && d->blank[frame-1].size<=d->capacity) {
            memcpy(d->frame,d->blank[frame-1].data,d->blank[frame-1].size); d->frame_size=d->blank[frame-1].size;
        }
    }
    pthread_mutex_unlock(&d->lock);
}

static void events(struct uvc_output *d) {
    struct v4l2_event event;
    while (ioctl(d->fd,VIDIOC_DQEVENT,&event)==0) {
        const struct uvc_event *u=(const struct uvc_event *)event.u.data;
        switch(event.type) {
        case UVC_EVENT_SETUP: setup(d,&u->req); break;
        case UVC_EVENT_DATA:
            // H.264 advertises one 640x480/15 profile: normalize the host's
            // probe to it. MJPEG follows the size and rate the host picks.
            if (d->h264) { if (d->pending_control==UVC_VS_PROBE_CONTROL) d->probe=d->commit; }
            else if (d->pending_control) negotiate(d,&u->data,d->pending_control==UVC_VS_COMMIT_CONTROL);
            d->pending_control=0;
            break;
        case UVC_EVENT_STREAMON:
            if (stream_on(d)<0) { pthread_mutex_lock(&d->lock); d->error=errno ? errno : EIO; pthread_mutex_unlock(&d->lock); }
            break;
        case UVC_EVENT_STREAMOFF: case UVC_EVENT_DISCONNECT: stream_off(d); break;
        }
    }
}

static void *worker(void *arg) {
    struct uvc_output *d=arg;
    for (;;) {
        pthread_mutex_lock(&d->lock); int stopped=d->stopped; pthread_mutex_unlock(&d->lock);
        if (stopped) break;
        uint64_t now=monotonic_ns();
        int queued=0; for(unsigned i=0;i<d->buffer_count;i++) queued+=d->queued[i];
        int ready=d->streaming && (d->h264 ? queued>0 : now>=d->next_frame);
        int timeout=d->h264 ? 10 : 100;
        if (d->streaming && !d->h264 && !ready) {
            uint64_t delay=(d->next_frame-now+999999)/1000000;
            if (delay<100) timeout=(int)delay;
        }
        struct pollfd p={.fd=d->fd,.events=POLLPRI | (ready ? POLLOUT : 0)};
        if (poll(&p,1,timeout)<0) { if(errno==EINTR) continue; break; }
        if (p.revents & POLLPRI) events(d);
        if (d->streaming && (p.revents & POLLOUT)) {
            struct v4l2_buffer b={.type=V4L2_BUF_TYPE_VIDEO_OUTPUT,.memory=V4L2_MEMORY_MMAP};
            if (ioctl(d->fd,VIDIOC_DQBUF,&b)==0) {
                pthread_mutex_lock(&d->lock); if (!(b.flags & V4L2_BUF_FLAG_ERROR)) d->frames++; pthread_mutex_unlock(&d->lock);
                d->queued[b.index]=0;
                if (!d->h264 && queue(d,&b)<0) stream_off(d);
                d->next_frame=monotonic_ns()+1000000000/d->fps;
            }
        }
    }
    stream_off(d); return NULL;
}

struct uvc_output *uvc_output_open(const char *path, unsigned width, unsigned height, unsigned fps, unsigned control_interface, unsigned streaming_interface, int h264) {
    int fd=open(path,O_RDWR|O_NONBLOCK|O_CLOEXEC);
    if(fd<0) return NULL;
    struct v4l2_capability cap;
    if(ioctl(fd,VIDIOC_QUERYCAP,&cap)<0 || strcmp((char *)cap.driver,"g_uvc") || !(cap.device_caps & V4L2_CAP_VIDEO_OUTPUT)) { close(fd); return NULL; }
    struct uvc_output *d=calloc(1,sizeof(*d));
    if(!d) { close(fd); return NULL; }
    d->h264=h264; d->needs_keyframe=1;
    d->fd=fd; d->width=width; d->height=height; d->fps=fps; d->capacity=width*height*2;
    if (!h264) {
        // MJPEG starts on the default frame and holds the largest one.
        d->width=mjpeg_modes[0].width; d->height=mjpeg_modes[0].height; fps=10000000/interval_shortest(); d->fps=fps;
        for (unsigned i=0;i<mjpeg_mode_count;i++) if (mjpeg_modes[i].width*mjpeg_modes[i].height*2>d->capacity) d->capacity=mjpeg_modes[i].width*mjpeg_modes[i].height*2;
    }
    d->control_interface=control_interface; d->streaming_interface=streaming_interface;
    d->frame=malloc(d->capacity);
    if(!d->frame) { close(fd); free(d); return NULL; }
    pthread_mutex_init(&d->lock,NULL);
    d->commit.bmHint=htole16(1); d->commit.bFormatIndex=1; d->commit.bFrameIndex=1;
    d->commit.dwFrameInterval=htole32(10000000/fps);
    d->commit.dwMaxVideoFrameSize=htole32(d->width*d->height*2);
    d->commit.dwMaxPayloadTransferSize=htole32(1024);
    d->commit.dwClockFrequency=htole32(48000000);
    d->commit.bmFramingInfo=3; d->commit.bPreferedVersion=1; d->commit.bMinVersion=1; d->commit.bMaxVersion=1;
    d->probe=d->commit;
    const unsigned types[]={UVC_EVENT_CONNECT,UVC_EVENT_DISCONNECT,UVC_EVENT_STREAMON,UVC_EVENT_STREAMOFF,UVC_EVENT_DATA,UVC_EVENT_SETUP};
    for(unsigned i=0;i<sizeof(types)/sizeof(types[0]);i++) {
        struct v4l2_event_subscription sub={.type=types[i]};
        if(ioctl(fd,VIDIOC_SUBSCRIBE_EVENT,&sub)<0) goto fail;
    }
    if(pthread_create(&d->thread,NULL,worker,d)) goto fail;
    return d;
fail:
    close(fd); pthread_mutex_destroy(&d->lock); free(d->frame); free(d); return NULL;
}

int uvc_output_frame(struct uvc_output *d, const void *data, size_t size, int keyframe) {
    pthread_mutex_lock(&d->lock);
    if(!size || size>d->capacity || d->error || d->stopped) { pthread_mutex_unlock(&d->lock); return -1; }
    if (!d->h264) {
        memcpy(d->frame,data,size); d->frame_size=size;
    } else {
    }
    pthread_mutex_unlock(&d->lock); return 0;
}

int uvc_output_jpeg(struct uvc_output *d, const void *data, size_t size, unsigned width, unsigned height) {
    pthread_mutex_lock(&d->lock);
    if(!size || size>d->capacity || d->error || d->stopped || d->h264) { pthread_mutex_unlock(&d->lock); return -1; }
    // The host changed the frame size: the browser follows within a second.
    if (width!=d->width || height!=d->height) { d->dropped++; pthread_mutex_unlock(&d->lock); return 1; }
    // The V4L2 buffers hold w*h*2 of the committed mode (stream_on); a larger
    // frame would fail VIDIOC_QBUF and stop the stream, so drop it alone.
    if (size>(size_t)d->width*d->height*2) { d->dropped++; pthread_mutex_unlock(&d->lock); return 1; }
    memcpy(d->frame,data,size); d->frame_size=size;
    pthread_mutex_unlock(&d->lock); return 0;
}

int uvc_output_set_blank(struct uvc_output *d, unsigned index, const void *data, size_t size) {
    if (index>=mjpeg_mode_count || !size || size>d->capacity) return -1;
    uint8_t *copy=malloc(size);
    if (!copy) return -1;
    memcpy(copy,data,size);
    pthread_mutex_lock(&d->lock);
    free(d->blank[index].data); d->blank[index]=(struct encoded_frame){copy,size};
    if (!d->h264 && d->width==mjpeg_modes[index].width && d->height==mjpeg_modes[index].height) {
        memcpy(d->frame,copy,size); d->frame_size=size;
    }
    pthread_mutex_unlock(&d->lock);
    return 0;
}

void uvc_output_blank(struct uvc_output *d) {
    pthread_mutex_lock(&d->lock);
    for (unsigned i=0;i<mjpeg_mode_count;i++) {
        if (d->h264 || d->width!=mjpeg_modes[i].width || d->height!=mjpeg_modes[i].height || !d->blank[i].size) continue;
        memcpy(d->frame,d->blank[i].data,d->blank[i].size); d->frame_size=d->blank[i].size;
    }
    pthread_mutex_unlock(&d->lock);
}

void uvc_output_mode(struct uvc_output *d, unsigned *width, unsigned *height, unsigned *fps) {
    pthread_mutex_lock(&d->lock); *width=d->width; *height=d->height; *fps=d->fps; pthread_mutex_unlock(&d->lock);
}

void uvc_output_mode_at(unsigned index, unsigned *width, unsigned *height) {
    *width=0; *height=0;
    if (index<mjpeg_mode_count) { *width=mjpeg_modes[index].width; *height=mjpeg_modes[index].height; }
}

unsigned uvc_output_mode_count(void) { return mjpeg_mode_count; }

void uvc_output_set_modes(unsigned count, const unsigned *widths, const unsigned *heights, unsigned interval_count, const unsigned *intervals) {
    if (count<1 || count>UVC_OUTPUT_MODES || interval_count<1 || interval_count>UVC_OUTPUT_INTERVALS) return;
    for (unsigned i=0;i<interval_count;i++) if (intervals[i]<100000 || intervals[i]>10000000) return;
    for (unsigned i=0;i<count;i++) { mjpeg_modes[i].width=widths[i]; mjpeg_modes[i].height=heights[i]; }
    mjpeg_mode_count=count;
    for (unsigned i=0;i<interval_count;i++) mjpeg_intervals[i]=intervals[i];
    mjpeg_interval_count=interval_count;
}

void uvc_output_reset(struct uvc_output *d) {
    pthread_mutex_lock(&d->lock); clear_pending(d); pthread_mutex_unlock(&d->lock);
}
void uvc_output_recovery(struct uvc_output *d, int *needs_keyframe, unsigned long long *dropped) {
    pthread_mutex_lock(&d->lock);
    *needs_keyframe=d->h264 && d->needs_keyframe; *dropped=d->dropped;
    pthread_mutex_unlock(&d->lock);
}

void uvc_output_status(struct uvc_output *d, int *streaming, int *error, unsigned long long *frames) {
    pthread_mutex_lock(&d->lock); *streaming=d->streaming; *error=d->error; *frames=d->frames; pthread_mutex_unlock(&d->lock);
}

void uvc_output_close(struct uvc_output *d) {
    pthread_mutex_lock(&d->lock); d->stopped=1; pthread_mutex_unlock(&d->lock);
    pthread_join(d->thread,NULL); close(d->fd);
    for (unsigned i=0;i<UVC_OUTPUT_MODES;i++) free(d->blank[i].data);
    pthread_mutex_destroy(&d->lock); free(d->frame); free(d);
}
