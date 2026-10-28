// The C side of toast_darwin.go: AppKit panels for the cards. Plain C, so
// the Go preamble can include it. Every toast_* call except toast_wake,
// toast_play and the toast_main_* pair runs on the main thread (it is called
// from Go code that the main thread itself called into: goToastSync,
// goToastTick, goToastRehome, goToastMouse).

#pragma once

#include <stdint.h>

typedef struct {
	double l, t, r, b; // top-down points, global (the primary screen's top-left is 0,0)
} ToastRect;

typedef struct {
	uint32_t bg, fg, muted, border, accent, faint, hover, button, primary, primaryHover, onPrimary; // 0xRRGGBB
} ToastColors;

// Card geometry the drawing needs, in points (layout.go's constants).
typedef struct {
	double stripe, iconLeft, iconSize, textLeft, textRight, titleRight, padTop;
	double titleH, titleSize, bodyGap, bodySize, buttonSize;
} ToastGeom;

// Mouse events forwarded to goToastMouse (toastRightDown only by toast_test_mouse;
// the view itself ignores a right press and acts on the release).
enum { toastMove = 1, toastLeave, toastDown, toastUp, toastRightUp, toastRightDown };

// Any thread.
void toast_wake(void);                  // queue goToastSync on the main thread
void toast_play(const void *wav, int n); // copies wav, plays it without waiting
int toast_main_thread(void);             // the caller is on the main thread
int toast_app_running(void);             // an NSApplication loop is running
void toast_main_run(void);               // main thread: run NSApp until toast_main_stop
void toast_main_stop(void);

// Main thread.
void toast_setup(void);
void toast_timer(int on);
ToastRect toast_work_area(int active, ToastRect old);
int toast_body_lines(const char *body, double width, double size);
double toast_text_width(const char *s, double size);
int toast_card_create(uint64_t key, const char *title, int captureExclude, double w, double h);
void toast_card_content(uint64_t key, const char *title, const char *body, int kind, int lines,
	const char *label0, const char *label1, int nlabels, ToastRect close, ToastRect b0, ToastRect b1,
	double radius, ToastColors colors, ToastGeom geom);
void toast_card_state(uint64_t key, int hot, int hovered, int clickable);
void toast_card_frame(uint64_t key, double x, double y, double w, double h, int raise);
void toast_card_alpha(uint64_t key, double a);
void toast_card_show(uint64_t key);
void toast_card_through(uint64_t key, int on);
void toast_card_destroy(uint64_t key);

// Tests (any thread but main; they wait for the main thread).
typedef struct {
	int exists, visible, key, main, ignoresMouse, sharingNone, onScreen, level, appActive, appHasKey;
	double alpha;
	ToastRect frame; // top-down
	ToastRect work;  // the visible frame of the card's screen, top-down
} ToastPanelInfo;
ToastPanelInfo toast_test_info(uint64_t key);
void toast_test_mouse(uint64_t key, int ev, double x, double y, int64_t tag);
int toast_test_frontmost_pid(void);
