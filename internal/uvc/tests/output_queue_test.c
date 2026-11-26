// Run on Linux: cc -pthread -o /tmp/uvc-queue-test internal/uvc/tests/output_queue_test.c && /tmp/uvc-queue-test
#include <assert.h>
#include <stdarg.h>
static int mock_ioctl(int fd, unsigned long request, ...);
#define ioctl mock_ioctl
#include "../output_linux.c"
#undef ioctl
static unsigned emitted;
static unsigned char copied[16];
static int mock_ioctl(int fd, unsigned long request, ...) {
    (void)fd;
    if (request==VIDIOC_QBUF) {
        va_list args; va_start(args,request);
        struct v4l2_buffer *b=va_arg(args,struct v4l2_buffer *); va_end(args);
        assert(b->bytesused==1); emitted++;
    }
    return 0;
}
int main(void) {
    struct uvc_output d={.h264=1,.needs_keyframe=1,.capacity=16,.buffer_count=1};
    d.buffers[0]=(struct output_buffer){copied,sizeof(copied)};
    pthread_mutex_init(&d.lock,NULL);
    unsigned char first=10,second=20;
    assert(uvc_output_frame(&d,&first,1,1)==1); // Host closed: no stale GOP retained.
    d.streaming=1;
    assert(uvc_output_frame(&d,&second,1,0)==1); // Require independent keyframe.
    assert(uvc_output_frame(&d,&first,1,1)==0);
    assert(uvc_output_frame(&d,&second,1,0)==0);
    struct v4l2_buffer b={.index=0};
    assert(queue(&d,&b)==0 && copied[0]==10);
    assert(queue(&d,&b)==0 && copied[0]==20); // Preserve order.
    assert(queue(&d,&b)==1 && emitted==2); // Never replay a predictive frame.
    for (unsigned i=0;i<FRAME_QUEUE_COUNT;i++) assert(uvc_output_frame(&d,&first,1,i==0)==0);
    assert(uvc_output_frame(&d,&second,1,0)==1 && d.pending_count==0 && d.needs_keyframe);
    assert(uvc_output_frame(&d,&first,1,1)==0); // Recover after overflow.
    uvc_output_reset(&d); assert(d.pending_count==0 && d.needs_keyframe);
    pthread_mutex_destroy(&d.lock);
    puts("H264 FIFO ordering, no replay, startup, overflow and reset passed");

    // MJPEG: a JPEG larger than the committed mode's buffer is dropped alone;
    // the stream and the frame before it stay.
    static unsigned char frame[1920*1080*2], jpeg[640*360*2+1];
    struct uvc_output m={.width=640,.height=360,.capacity=sizeof(frame),.frame=frame};
    pthread_mutex_init(&m.lock,NULL);
    assert(uvc_output_jpeg(&m,jpeg,640*360*2,640,360)==0 && m.frame_size==640*360*2);
    assert(uvc_output_jpeg(&m,jpeg,sizeof(jpeg),640,360)==1 && m.dropped==1 && m.frame_size==640*360*2 && !m.error);
    assert(uvc_output_jpeg(&m,jpeg,100,640,360)==0 && m.frame_size==100);
    pthread_mutex_destroy(&m.lock);
    puts("MJPEG oversize frame dropped, stream kept");
    return 0;
}
