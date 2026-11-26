#ifndef HOME_ICONS_H
#define HOME_ICONS_H

#include <lvgl.h>

// The icons of the home screen's function list, by the name that the Go side
// gives each function. They are alpha-only: the screen colours them.
typedef struct {
    const char *name;
    const lv_image_dsc_t *image;
} home_icon_t;

extern const home_icon_t home_icons[];
extern const int home_icons_size;

#endif
