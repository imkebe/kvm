#ifndef VIDEO_DAEMON_VIDEO_H
#define VIDEO_DAEMON_VIDEO_H

#include <stdint.h>

/**
 * @brief Initialize the video subsystem
 *
 * @return int 0 on success, -1 on failure
 */
int video_init(float quality_factor);

/**
 * @brief Shutdown the video subsystem
 */
void video_shutdown();

/**
 * @brief Run the detect format thread
 *
 * @param arg The argument to pass to the thread
 * @return void* The result of the thread
 */
void *run_detect_format(void *arg);

/**
 * @brief Start the video streaming
 */
void video_start_streaming();

/**
 * @brief Stop the video streaming
 */
void video_stop_streaming();

/**
 * @brief Get the streaming status of the video
 *
 * @return uint8_t 1 if the video streaming is active, 2 if the video streaming is stopping, 0 otherwise
 */
uint8_t video_get_streaming_status();

/**
 * @brief Set the quality factor of the video
 *
 * @param factor The quality factor to set
 */
void video_set_quality_factor(float factor);

/**
 * @brief Get the quality factor of the video
 *
 * @return float The quality factor of the video
 */
float video_get_quality_factor();

/**
 * @brief Set the codec type (0 = H.264, 1 = H.265)
 */
void video_set_codec_type(int type);

/**
 * @brief Get the codec type (0 = H.264, 1 = H.265)
 */
int video_get_codec_type();

/**
 * @brief Copy the next captured frame (YUYV 4:2:2, limited range)
 *
 * Waits up to timeout_ms for the capture loop, which runs only while video
 * streams. On success *data is a malloc'd buffer of stride * height bytes
 * that the caller frees.
 *
 * @return 0 on success, -1 on timeout, -2 if the frame could not be copied
 */
int video_snapshot(uint8_t **data, uint32_t *width, uint32_t *height, uint32_t *stride, int timeout_ms);

#endif //VIDEO_DAEMON_VIDEO_H
