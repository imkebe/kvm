#ifndef JETKVM_UVC_OUTPUT_H
#define JETKVM_UVC_OUTPUT_H
#include <stddef.h>
#define UVC_OUTPUT_MODES 8
#define UVC_OUTPUT_INTERVALS 8
struct uvc_output;
struct uvc_output *uvc_output_open(const char *path, unsigned width, unsigned height, unsigned fps, unsigned control_interface, unsigned streaming_interface, int h264);
int uvc_output_frame(struct uvc_output *output, const void *data, size_t size, int keyframe);
int uvc_output_jpeg(struct uvc_output *output, const void *data, size_t size, unsigned width, unsigned height);
int uvc_output_set_blank(struct uvc_output *output, unsigned index, const void *data, size_t size);
void uvc_output_blank(struct uvc_output *output);
void uvc_output_mode(struct uvc_output *output, unsigned *width, unsigned *height, unsigned *fps);
// Sets the MJPEG frame sizes that the gadget offers, in the order of their
// frame index, and the frame intervals (100 ns units) every size offers.
// Call it before open.
void uvc_output_set_modes(unsigned count, const unsigned *widths, const unsigned *heights, unsigned interval_count, const unsigned *intervals);
unsigned uvc_output_mode_count(void);
void uvc_output_mode_at(unsigned index, unsigned *width, unsigned *height);
void uvc_output_reset(struct uvc_output *output);
void uvc_output_recovery(struct uvc_output *output, int *needs_keyframe, unsigned long long *dropped);
void uvc_output_status(struct uvc_output *output, int *streaming, int *error, unsigned long long *frames);
void uvc_output_close(struct uvc_output *output);
#endif
