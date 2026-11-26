#include "home_ext.h"

#include <pthread.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include <lvgl.h>

#include "ctrl.h"
#include "home_icons.h"
#include "ui/images.h"
#include "ui/screens.h"
#include "ui/styles.h"

#define MAX_CHIPS 14
#define MAX_CARDS 8
// The pages of one card: the summary on the home screen and its details.
#define MAX_PAGES 6
#define MAX_SERIES 2
#define MAX_POINTS 64
#define STATUS_LEN 256
#define CARDS_LEN 16384
#define DETAILS_LEN 1024

#define COLOR_GREEN 0xff22c55e
#define COLOR_GRAY 0xff525252
#define COLOR_YELLOW 0xffeab308
#define COLOR_RED 0xffef4444
#define COLOR_BLUE 0xff3b82f6
#define COLOR_TITLE 0xffa3a3a3
#define COLOR_GRID 0xff262626

// The details that are open: a card, the network card, or none.
#define DETAILS_NONE -2
#define DETAILS_NETWORK -1

// The variables as the Go side set them, and whether the screen shows them.
static pthread_mutex_t lock = PTHREAD_MUTEX_INITIALIZER;
static char status_text[STATUS_LEN];
static char cards_text[CARDS_LEN];
static char details_text[DETAILS_LEN];
static bool status_dirty, cards_dirty, details_dirty;
// The card to show, when the Go side asks for one; -1 otherwise.
static int page_wanted = -1;

// One page: a title, a text, and a chart when the page has one.
typedef struct {
    lv_obj_t *obj;
    lv_obj_t *title;
    lv_obj_t *body;
    lv_obj_t *chart;
    lv_chart_series_t *series[MAX_SERIES];
} page_t;

static lv_obj_t *carousel;
static lv_obj_t *chips_row;
static lv_obj_t *chips[MAX_CHIPS];
static bool chip_is_icon[MAX_CHIPS];
static lv_obj_t *usb_icon;
static lv_obj_t *hdmi_icon;
static page_t card_pages[MAX_CARDS];
static lv_obj_t *details_panel;
static lv_obj_t *details_carousel;
static page_t details_pages[MAX_PAGES];
static int details_open = DETAILS_NONE;

// The cards as the screen shows them: the text of each page of each card.
// The pointers go into cards_model and network_model.
static char cards_model[CARDS_LEN];
static char network_model[DETAILS_LEN];
static char *card_text[MAX_CARDS][MAX_PAGES];
static int card_page_count[MAX_CARDS];
static int card_count;
static char *network_text[MAX_PAGES];
static int network_page_count;

static void set_text(char *to, size_t size, bool *dirty, const char *value) {
    pthread_mutex_lock(&lock);
    if (strncmp(to, value, size) != 0) {
        strncpy(to, value, size - 1);
        to[size - 1] = 0;
        *dirty = true;
    }
    pthread_mutex_unlock(&lock);
}

void home_ext_set_status(const char *value) { set_text(status_text, STATUS_LEN, &status_dirty, value); }
void home_ext_set_cards(const char *value) { set_text(cards_text, CARDS_LEN, &cards_dirty, value); }
void home_ext_set_details(const char *value) { set_text(details_text, DETAILS_LEN, &details_dirty, value); }
void home_ext_set_page(const char *value) {
    int page = -1;
    sscanf(value, "%d", &page);
    pthread_mutex_lock(&lock);
    page_wanted = page;
    pthread_mutex_unlock(&lock);
}
const char *home_ext_get_status(void) { return status_text; }
const char *home_ext_get_cards(void) { return cards_text; }
const char *home_ext_get_details(void) { return details_text; }

// A state letter from the Go side: g active, y enabled but not active,
// r error, b a second chart line, anything else off.
static lv_color_t state_color(char state) {
    switch (state) {
    case 'g': return lv_color_hex(COLOR_GREEN);
    case 'y': return lv_color_hex(COLOR_YELLOW);
    case 'r': return lv_color_hex(COLOR_RED);
    case 'b': return lv_color_hex(COLOR_BLUE);
    }
    return lv_color_hex(COLOR_GRAY);
}

static void plain(lv_obj_t *obj) {
    lv_obj_set_style_pad_all(obj, 0, LV_PART_MAIN | LV_STATE_DEFAULT);
    lv_obj_set_style_bg_opa(obj, 0, LV_PART_MAIN | LV_STATE_DEFAULT);
    lv_obj_set_style_border_width(obj, 0, LV_PART_MAIN | LV_STATE_DEFAULT);
    lv_obj_set_style_radius(obj, 0, LV_PART_MAIN | LV_STATE_DEFAULT);
    lv_obj_set_scrollbar_mode(obj, LV_SCROLLBAR_MODE_OFF);
}

static lv_obj_t *icon(lv_obj_t *parent, const lv_image_dsc_t *image) {
    lv_obj_t *obj = lv_image_create(parent);
    lv_image_set_src(obj, image);
    lv_obj_set_style_image_recolor_opa(obj, 255, LV_PART_MAIN | LV_STATE_DEFAULT);
    lv_obj_set_style_image_recolor(obj, lv_color_hex(COLOR_GRAY), LV_PART_MAIN | LV_STATE_DEFAULT);
    return obj;
}

// A carousel is a row of pages, each as wide as the carousel. It scrolls
// sideways one page at a time; an up or down swipe stays a gesture for the
// screen.
static lv_obj_t *build_carousel(lv_obj_t *parent) {
    lv_obj_t *obj = lv_obj_create(parent);
    plain(obj);
    lv_obj_set_width(obj, LV_PCT(100));
    lv_obj_set_flex_grow(obj, 1);
    lv_obj_set_layout(obj, LV_LAYOUT_FLEX);
    lv_obj_set_flex_flow(obj, LV_FLEX_FLOW_ROW);
    lv_obj_set_style_pad_column(obj, 0, LV_PART_MAIN | LV_STATE_DEFAULT);
    lv_obj_set_scroll_dir(obj, LV_DIR_HOR);
    lv_obj_set_scroll_snap_x(obj, LV_SCROLL_SNAP_START);
    lv_obj_add_flag(obj, LV_OBJ_FLAG_SCROLL_ONE);
    lv_obj_clear_flag(obj, LV_OBJ_FLAG_SCROLL_ELASTIC);
    return obj;
}

static void build_page(page_t *page, lv_obj_t *parent, lv_event_cb_t on_tap, intptr_t index) {
    lv_obj_t *obj = lv_obj_create(parent);
    plain(obj);
    lv_obj_set_size(obj, LV_PCT(100), LV_PCT(100));
    lv_obj_set_layout(obj, LV_LAYOUT_FLEX);
    lv_obj_set_flex_flow(obj, LV_FLEX_FLOW_COLUMN);
    lv_obj_set_style_pad_row(obj, 2, LV_PART_MAIN | LV_STATE_DEFAULT);
    lv_obj_clear_flag(obj, LV_OBJ_FLAG_SCROLLABLE);
    lv_obj_add_flag(obj, LV_OBJ_FLAG_CLICKABLE);
    lv_obj_add_event_cb(obj, on_tap, LV_EVENT_SHORT_CLICKED, (void *)index);

    page->obj = obj;
    page->title = lv_label_create(obj);
    add_style_label_font16(page->title);
    lv_obj_set_style_text_color(page->title, lv_color_hex(COLOR_TITLE), LV_PART_MAIN | LV_STATE_DEFAULT);
    page->body = lv_label_create(obj);
    add_style_label_font16(page->body);
    lv_obj_set_width(page->body, LV_PCT(100));
    lv_label_set_long_mode(page->body, LV_LABEL_LONG_CLIP);
    lv_obj_set_style_text_align(page->body, LV_TEXT_ALIGN_LEFT, LV_PART_MAIN | LV_STATE_DEFAULT);

    // The chart takes the height that the text leaves. It shows lines only.
    lv_obj_t *chart = lv_chart_create(obj);
    lv_obj_set_width(chart, LV_PCT(100));
    lv_obj_set_flex_grow(chart, 1);
    lv_chart_set_type(chart, LV_CHART_TYPE_LINE);
    lv_chart_set_div_line_count(chart, 3, 0);
    lv_obj_set_style_pad_all(chart, 0, LV_PART_MAIN | LV_STATE_DEFAULT);
    lv_obj_set_style_radius(chart, 0, LV_PART_MAIN | LV_STATE_DEFAULT);
    lv_obj_set_style_bg_opa(chart, 0, LV_PART_MAIN | LV_STATE_DEFAULT);
    lv_obj_set_style_border_width(chart, 1, LV_PART_MAIN | LV_STATE_DEFAULT);
    lv_obj_set_style_border_color(chart, lv_color_hex(COLOR_GRID), LV_PART_MAIN | LV_STATE_DEFAULT);
    lv_obj_set_style_line_color(chart, lv_color_hex(COLOR_GRID), LV_PART_MAIN | LV_STATE_DEFAULT);
    lv_obj_set_style_line_width(chart, 2, LV_PART_ITEMS | LV_STATE_DEFAULT);
    lv_obj_set_style_width(chart, 0, LV_PART_INDICATOR | LV_STATE_DEFAULT);
    lv_obj_set_style_height(chart, 0, LV_PART_INDICATOR | LV_STATE_DEFAULT);
    lv_obj_clear_flag(chart, LV_OBJ_FLAG_CLICKABLE | LV_OBJ_FLAG_SCROLLABLE);
    for (int i = 0; i < MAX_SERIES; i++) {
        page->series[i] = lv_chart_add_series(chart, lv_color_hex(COLOR_GREEN), LV_CHART_AXIS_PRIMARY_Y);
    }
    page->chart = chart;
}

// A chart line of a page is "~max;c;v,v,v" and can go on with ";c;v,v,v" for
// a second line: max is the top of the scale and c the colour letter.
static void apply_chart(page_t *page, char *spec) {
    char *save = NULL;
    char *field = strtok_r(spec, ";", &save);
    int max = field != NULL ? atoi(field) : 0;
    lv_chart_set_axis_range(page->chart, LV_CHART_AXIS_PRIMARY_Y, 0, max > 0 ? max : 1);
    for (int i = 0; i < MAX_SERIES; i++) {
        char *color = strtok_r(NULL, ";", &save);
        char *values = color != NULL ? strtok_r(NULL, ";", &save) : NULL;
        if (values == NULL) {
            lv_chart_hide_series(page->chart, page->series[i], true);
            continue;
        }
        int points[MAX_POINTS];
        int count = 0;
        char *inner = NULL;
        for (char *v = strtok_r(values, ",", &inner); v != NULL && count < MAX_POINTS; v = strtok_r(NULL, ",", &inner)) {
            points[count++] = atoi(v);
        }
        if (i == 0) {
            lv_chart_set_point_count(page->chart, count > 1 ? count : 2);
        }
        lv_chart_set_series_color(page->chart, page->series[i], state_color(color[0]));
        lv_chart_hide_series(page->chart, page->series[i], false);
        lv_chart_set_all_values(page->chart, page->series[i], 0);
        for (int p = 0; p < count; p++) {
            lv_chart_set_series_value_by_id(page->chart, page->series[i], p, points[p]);
        }
    }
    lv_chart_refresh(page->chart);
}

// The first line of a page is its title. A line that starts with '~' is the
// chart, and the other lines are the text. suffix follows the title.
static void apply_page(page_t *page, const char *text, const char *suffix) {
    static char copy[CARDS_LEN];
    static char body[512];
    strncpy(copy, text, sizeof(copy) - 1);
    copy[sizeof(copy) - 1] = 0;
    char *chart = NULL;
    size_t used = 0;
    body[0] = 0;
    char *line = copy;
    char *rest = strchr(line, '\n');
    if (rest != NULL) {
        *rest++ = 0;
    }
    char title[80];
    snprintf(title, sizeof(title), "%s%s", line, suffix);
    lv_label_set_text(page->title, title);
    while (rest != NULL) {
        line = rest;
        rest = strchr(line, '\n');
        if (rest != NULL) {
            *rest++ = 0;
        }
        if (line[0] == '~') {
            chart = line + 1;
        } else if (used + strlen(line) + 2 < sizeof(body)) {
            used += (size_t)snprintf(body + used, sizeof(body) - used, "%s%s", used > 0 ? "\n" : "", line);
        }
    }
    lv_label_set_text(page->body, body);
    if (body[0] == 0) {
        lv_obj_add_flag(page->body, LV_OBJ_FLAG_HIDDEN);
    } else {
        lv_obj_clear_flag(page->body, LV_OBJ_FLAG_HIDDEN);
    }
    if (chart == NULL) {
        lv_obj_add_flag(page->chart, LV_OBJ_FLAG_HIDDEN);
    } else {
        lv_obj_clear_flag(page->chart, LV_OBJ_FLAG_HIDDEN);
        apply_chart(page, chart);
    }
}

static void details_hide(lv_event_t *e) {
    (void)e;
    details_open = DETAILS_NONE;
    lv_obj_add_flag(details_panel, LV_OBJ_FLAG_HIDDEN);
}

// Shows the detail pages of the open card. The first page of a card is its
// summary on the home screen, so its details start at the second.
static void details_apply(void) {
    char **pages = NULL;
    int count = 0;
    if (details_open == DETAILS_NETWORK) {
        pages = network_text;
        count = network_page_count;
    } else if (details_open >= 0 && details_open < card_count) {
        pages = &card_text[details_open][1];
        count = card_page_count[details_open] - 1;
    }
    if (count <= 0) {
        details_hide(NULL);
        return;
    }
    for (int i = 0; i < MAX_PAGES; i++) {
        if (i >= count) {
            if (details_pages[i].obj != NULL) {
                lv_obj_add_flag(details_pages[i].obj, LV_OBJ_FLAG_HIDDEN);
            }
            continue;
        }
        if (details_pages[i].obj == NULL) {
            build_page(&details_pages[i], details_carousel, details_hide, i);
        }
        char suffix[16] = "";
        if (count > 1) {
            snprintf(suffix, sizeof(suffix), "  %d/%d", i + 1, count);
        }
        apply_page(&details_pages[i], pages[i], suffix);
        lv_obj_clear_flag(details_pages[i].obj, LV_OBJ_FLAG_HIDDEN);
    }
}

static void details_show(lv_event_t *e) {
    int card = (int)(intptr_t)lv_event_get_user_data(e);
    if (card >= 0 && (card >= card_count || card_page_count[card] < 2)) {
        return;
    }
    details_open = card;
    details_apply();
    if (details_open == DETAILS_NONE) {
        return;
    }
    lv_obj_clear_flag(details_panel, LV_OBJ_FLAG_HIDDEN);
    lv_obj_scroll_to_x(details_carousel, 0, LV_ANIM_OFF);
}

static void build_home_carousel(lv_obj_t *column) {
    carousel = build_carousel(column);
    lv_obj_move_to_index(carousel, 1);

    // The first page is the network card of the EEZ project. A tap on it
    // opens the network details.
    lv_obj_t *first = objects.home_info_container;
    lv_obj_set_parent(first, carousel);
    lv_obj_set_size(first, LV_PCT(100), LV_PCT(100));
    lv_obj_set_flex_grow(first, 0);
    lv_obj_clear_flag(first, LV_OBJ_FLAG_SCROLLABLE);
    lv_obj_add_flag(first, LV_OBJ_FLAG_CLICKABLE);
    lv_obj_add_event_cb(first, details_show, LV_EVENT_SHORT_CLICKED, (void *)(intptr_t)DETAILS_NETWORK);
}

static void build_status(lv_obj_t *column) {
    chips_row = lv_obj_create(column);
    plain(chips_row);
    lv_obj_set_size(chips_row, LV_PCT(100), LV_SIZE_CONTENT);
    lv_obj_set_layout(chips_row, LV_LAYOUT_FLEX);
    lv_obj_set_flex_flow(chips_row, LV_FLEX_FLOW_ROW_WRAP);
    lv_obj_set_style_pad_column(chips_row, 3, LV_PART_MAIN | LV_STATE_DEFAULT);
    lv_obj_set_style_pad_row(chips_row, 4, LV_PART_MAIN | LV_STATE_DEFAULT);
    lv_obj_clear_flag(chips_row, LV_OBJ_FLAG_SCROLLABLE | LV_OBJ_FLAG_CLICKABLE);
    lv_obj_move_to_index(chips_row, lv_obj_get_index(objects.home_status_container));

    // The Go side no longer writes the two status texts; they stay, hidden,
    // because the object table still names them.
    lv_obj_add_flag(objects.usb_status, LV_OBJ_FLAG_HIDDEN);
    lv_obj_add_flag(objects.hdmi_status, LV_OBJ_FLAG_HIDDEN);
    lv_obj_set_style_pad_column(objects.home_status_container, 12, LV_PART_MAIN | LV_STATE_DEFAULT);
    usb_icon = icon(objects.home_status_container, &img_usb);
    hdmi_icon = icon(objects.home_status_container, &img_hdmi);
}

static void build_details(void) {
    details_panel = lv_obj_create(objects.home_screen);
    lv_obj_add_flag(details_panel, LV_OBJ_FLAG_FLOATING | LV_OBJ_FLAG_HIDDEN);
    lv_obj_clear_flag(details_panel, LV_OBJ_FLAG_SCROLLABLE);
    lv_obj_set_pos(details_panel, -44, -24);
    lv_obj_set_size(details_panel, 300, 240);
    lv_obj_set_style_bg_color(details_panel, lv_color_hex(0xff000000), LV_PART_MAIN | LV_STATE_DEFAULT);
    lv_obj_set_style_bg_opa(details_panel, 255, LV_PART_MAIN | LV_STATE_DEFAULT);
    lv_obj_set_style_border_width(details_panel, 0, LV_PART_MAIN | LV_STATE_DEFAULT);
    lv_obj_set_style_radius(details_panel, 0, LV_PART_MAIN | LV_STATE_DEFAULT);
    lv_obj_set_style_pad_left(details_panel, 44, LV_PART_MAIN | LV_STATE_DEFAULT);
    lv_obj_set_style_pad_right(details_panel, 24, LV_PART_MAIN | LV_STATE_DEFAULT);
    lv_obj_set_style_pad_top(details_panel, 24, LV_PART_MAIN | LV_STATE_DEFAULT);
    lv_obj_set_style_pad_bottom(details_panel, 24, LV_PART_MAIN | LV_STATE_DEFAULT);
    lv_obj_set_layout(details_panel, LV_LAYOUT_FLEX);
    lv_obj_set_flex_flow(details_panel, LV_FLEX_FLOW_COLUMN);
    lv_obj_add_event_cb(details_panel, details_hide, LV_EVENT_SHORT_CLICKED, NULL);
    details_carousel = build_carousel(details_panel);
}

void home_ext_init(void) {
    if (carousel != NULL || objects.home_info_container == NULL) {
        return;
    }
    lv_obj_t *column = lv_obj_get_parent(objects.home_info_container);
    lv_obj_set_style_pad_row(column, 6, LV_PART_MAIN | LV_STATE_DEFAULT);
    lv_obj_clear_flag(column, LV_OBJ_FLAG_SCROLLABLE);
    build_home_carousel(column);
    build_status(column);
    build_details();
}

static const lv_image_dsc_t *chip_icon(const char *name) {
    for (int i = 0; i < home_icons_size; i++) {
        if (strcmp(home_icons[i].name, name) == 0) {
            return home_icons[i].image;
        }
    }
    return NULL;
}

// A chip is the icon of its name, or the name itself when it has no icon.
static void apply_chip(int i, const char *name, lv_color_t color) {
    const lv_image_dsc_t *image = chip_icon(name);
    if (chips[i] != NULL && chip_is_icon[i] != (image != NULL)) {
        lv_obj_delete(chips[i]);
        chips[i] = NULL;
    }
    if (chips[i] == NULL) {
        chip_is_icon[i] = image != NULL;
        if (image != NULL) {
            chips[i] = icon(chips_row, image);
        } else {
            chips[i] = lv_label_create(chips_row);
            add_style_label_font16(chips[i]);
        }
        lv_obj_move_to_index(chips[i], i);
    }
    if (image != NULL) {
        lv_image_set_src(chips[i], image);
        lv_obj_set_style_image_recolor(chips[i], color, LV_PART_MAIN | LV_STATE_DEFAULT);
    } else {
        lv_label_set_text(chips[i], name);
        lv_obj_set_style_text_color(chips[i], color, LV_PART_MAIN | LV_STATE_DEFAULT);
    }
    lv_obj_clear_flag(chips[i], LV_OBJ_FLAG_HIDDEN);
}

// The status is a list of NAME:state separated by spaces. USB and HDMI
// colour the two cable icons; each other name is a chip, in the order given.
static void apply_status(char *text) {
    int count = 0;
    char *save = NULL;
    for (char *token = strtok_r(text, " ", &save); token != NULL; token = strtok_r(NULL, " ", &save)) {
        char *colon = strrchr(token, ':');
        if (colon == NULL || colon[1] == 0) {
            continue;
        }
        *colon = 0;
        lv_color_t color = state_color(colon[1]);
        if (strcmp(token, "USB") == 0) {
            lv_obj_set_style_image_recolor(usb_icon, color, LV_PART_MAIN | LV_STATE_DEFAULT);
        } else if (strcmp(token, "HDMI") == 0) {
            lv_obj_set_style_image_recolor(hdmi_icon, color, LV_PART_MAIN | LV_STATE_DEFAULT);
        } else if (count < MAX_CHIPS) {
            apply_chip(count++, token, color);
        }
    }
    for (int i = count; i < MAX_CHIPS; i++) {
        if (chips[i] != NULL) {
            lv_obj_add_flag(chips[i], LV_OBJ_FLAG_HIDDEN);
        }
    }
}

// split cuts text at each separator and returns the number of parts.
static int split(char *text, char separator, char **parts, int max) {
    int count = 0;
    while (text != NULL && *text != 0 && count < max) {
        parts[count++] = text;
        text = strchr(text, separator);
        if (text != NULL) {
            *text++ = 0;
        }
    }
    return count;
}

// The cards are separated by a form feed, and the pages of a card by a
// vertical tab. The first page of a card shows on the home screen; a tap
// opens the others.
static void apply_cards(void) {
    char *cards[MAX_CARDS];
    card_count = split(cards_model, '\f', cards, MAX_CARDS);
    for (int i = 0; i < MAX_CARDS; i++) {
        if (i >= card_count) {
            card_page_count[i] = 0;
            if (card_pages[i].obj != NULL) {
                lv_obj_add_flag(card_pages[i].obj, LV_OBJ_FLAG_HIDDEN);
            }
            continue;
        }
        card_page_count[i] = split(cards[i], '\v', card_text[i], MAX_PAGES);
        if (card_page_count[i] == 0) {
            continue;
        }
        if (card_pages[i].obj == NULL) {
            build_page(&card_pages[i], carousel, details_show, i);
        }
        // The mark tells that a tap opens more.
        char suffix[24];
        snprintf(suffix, sizeof(suffix), "  %d/%d%s", i + 2, card_count + 1, card_page_count[i] > 1 ? "  >" : "");
        apply_page(&card_pages[i], card_text[i][0], suffix);
        lv_obj_clear_flag(card_pages[i].obj, LV_OBJ_FLAG_HIDDEN);
    }
}

// statistics_shown reports whether the screen shows a card with statistics:
// a card other than the network one, or the details of one.
static bool statistics_shown(void) {
    if (lv_screen_active() != objects.home_screen) {
        return false;
    }
    if (details_open != DETAILS_NONE) {
        return details_open != DETAILS_NETWORK;
    }
    int32_t width = lv_obj_get_width(carousel);
    return width > 0 && lv_obj_get_scroll_x(carousel) >= width / 2;
}

// The app samples its statistics only while the screen shows them. It gets
// "homeStats" when they come into view and every 5 seconds while they stay,
// and "homeIdle" when they leave.
static void report_statistics_shown(void) {
    static bool reported = false;
    static uint32_t reported_at = 0;
    bool shown = statistics_shown();
    uint32_t now = lv_tick_get();
    if (shown == reported && !(shown && now - reported_at >= 5000)) {
        return;
    }
    reported = shown;
    reported_at = now;
    jetkvm_call_rpc_handler(shown ? "homeStats" : "homeIdle", "");
}

void home_ext_tick(void) {
    if (carousel == NULL) {
        return;
    }
    report_statistics_shown();
    static char copy[STATUS_LEN];
    bool status = false, cards = false, details = false;
    pthread_mutex_lock(&lock);
    if (status_dirty) {
        status_dirty = false;
        status = true;
        memcpy(copy, status_text, STATUS_LEN);
    }
    if (cards_dirty) {
        cards_dirty = false;
        cards = true;
        memcpy(cards_model, cards_text, CARDS_LEN);
    }
    if (details_dirty) {
        details_dirty = false;
        details = true;
        memcpy(network_model, details_text, DETAILS_LEN);
    }
    int page = page_wanted;
    page_wanted = -1;
    pthread_mutex_unlock(&lock);

    if (status) {
        apply_status(copy);
    }
    if (cards) {
        apply_cards();
    }
    if (details) {
        network_page_count = split(network_model, '\v', network_text, MAX_PAGES);
    }
    if ((cards || details) && details_open != DETAILS_NONE) {
        details_apply();
    }
    if (page >= 100) {
        // 100 and up open the details of a card, for a look from outside:
        // 100 is the network card.
        details_open = page - 101;
        details_apply();
        if (details_open != DETAILS_NONE) {
            lv_obj_clear_flag(details_panel, LV_OBJ_FLAG_HIDDEN);
        }
    } else if (page >= 0 && page < (int)lv_obj_get_child_count(carousel)) {
        details_hide(NULL);
        lv_obj_scroll_to_view(lv_obj_get_child(carousel, page), LV_ANIM_OFF);
    }
}
