#ifndef HOME_EXT_H
#define HOME_EXT_H

// The home screen beyond what the EEZ project draws: the statistics cards,
// the list of USB functions and the two connection icons. The Go side sets
// their content through the UI variables home_status, home_cards and
// home_details.

// home_ext_init builds the objects. Call it once, after ui_init.
void home_ext_init(void);
// home_ext_tick applies new variable values. Call it on the UI thread.
void home_ext_tick(void);

void home_ext_set_status(const char *value);
void home_ext_set_cards(const char *value);
void home_ext_set_details(const char *value);
// home_ext_set_page shows the card with the given number, from 0.
void home_ext_set_page(const char *value);
const char *home_ext_get_status(void);
const char *home_ext_get_cards(void);
const char *home_ext_get_details(void);

#endif
