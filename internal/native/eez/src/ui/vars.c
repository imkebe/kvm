#include <string.h>
#include <stdio.h>
#include <lvgl.h>
#include "vars.h"

char app_version[100] = { 0 };
char system_version[100] = { 0 };
char lvgl_version[32] = { 0 };
char main_screen[32] = "home_screen";

// The home screen's status, cards and details: see cgo/home_ext.h.
#include "../../../cgo/home_ext.h"

const char *get_var_home_status() { return home_ext_get_status(); }
void set_var_home_status(const char *value) { home_ext_set_status(value); }
const char *get_var_home_cards() { return home_ext_get_cards(); }
void set_var_home_cards(const char *value) { home_ext_set_cards(value); }
const char *get_var_home_details() { return home_ext_get_details(); }
void set_var_home_details(const char *value) { home_ext_set_details(value); }
const char *get_var_home_page() { return ""; }
void set_var_home_page(const char *value) { home_ext_set_page(value); }

const char *get_var_app_version() {
    return app_version;
}

const char *get_var_system_version() {
    return system_version;
}

const char *get_var_lvgl_version() {
    if (lvgl_version[0] == '\0') {
        char buf[32];
        sprintf(buf, "%d.%d.%d", LVGL_VERSION_MAJOR, LVGL_VERSION_MINOR, LVGL_VERSION_PATCH);
        
        
        strncpy(lvgl_version, buf, sizeof(lvgl_version) / sizeof(char));
        app_version[sizeof(lvgl_version) / sizeof(char) - 1] = 0;
    }
    return lvgl_version;
}

void set_var_app_version(const char *value) {
    strncpy(app_version, value, sizeof(app_version) / sizeof(char));
    app_version[sizeof(app_version) / sizeof(char) - 1] = 0;
}

void set_var_system_version(const char *value) {
    strncpy(system_version, value, sizeof(system_version) / sizeof(char));
    system_version[sizeof(system_version) / sizeof(char) - 1] = 0;
}

void set_var_lvgl_version(const char *value) {}

void set_var_main_screen(const char *value) {
    strncpy(main_screen, value, sizeof(main_screen) / sizeof(char));
    main_screen[sizeof(main_screen) / sizeof(char) - 1] = 0;
}

const char *get_var_main_screen() {
    return main_screen;
}