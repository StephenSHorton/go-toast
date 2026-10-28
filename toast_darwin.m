//go:build darwin && cgo

// AppKit side of the macOS cards; see toast_darwin.go for the design.

#import <AppKit/AppKit.h>
#import <AVFoundation/AVFoundation.h>
#include "toast_darwin.h"
#include "_cgo_export.h"

static NSColor *rgb(uint32_t c) {
	return [NSColor colorWithSRGBRed:((c >> 16) & 255) / 255.0
	                           green:((c >> 8) & 255) / 255.0
	                            blue:(c & 255) / 255.0
	                           alpha:1];
}

// The primary screen's height: AppKit's global coordinates are bottom-up from
// its bottom-left corner, the Go side's are top-down from its top-left.
static CGFloat primaryHeight(void) {
	NSArray<NSScreen *> *ss = [NSScreen screens];
	return ss.count ? ss[0].frame.size.height : 0;
}

static ToastRect topDown(NSRect r) {
	CGFloat H = primaryHeight();
	return (ToastRect){NSMinX(r), H - NSMaxY(r), NSMaxX(r), H - NSMinY(r)};
}

static NSRect fromRect(ToastRect r) { return NSMakeRect(r.l, r.t, r.r - r.l, r.b - r.t); }

// ToastPanel is never key or main, so it can't take focus from anything.
@interface ToastPanel : NSPanel
@end
@implementation ToastPanel
- (BOOL)canBecomeKeyWindow { return NO; }
- (BOOL)canBecomeMainWindow { return NO; }
@end

@interface ToastView : NSView
@property uint64_t key;
@property(copy) NSString *title, *body;
@property(copy) NSArray<NSString *> *labels;
@property int kind, lines, hot, hovered, clickable, ctrlDown;
@property NSRect closeR, b0, b1;
@property CGFloat radius;
@property ToastColors colors;
@property ToastGeom geom;
@end

static const int partClose = -2, partBody = -1;

static NSFont *font(CGFloat size, BOOL bold) {
	return [NSFont systemFontOfSize:size weight:bold ? NSFontWeightSemibold : NSFontWeightRegular];
}

static CGFloat lineHeight(NSFont *f) { return ceil(f.ascender - f.descender + f.leading); }

// tinted draws a template image in one color, at whatever scale it lands on.
static NSImage *tinted(NSImage *src, NSColor *c) {
	return [NSImage imageWithSize:src.size flipped:NO drawingHandler:^BOOL(NSRect r) {
		[src drawInRect:r];
		[c set];
		NSRectFillUsingOperation(r, NSCompositingOperationSourceAtop);
		return YES;
	}];
}

static NSString *symbolFor(int kind) {
	switch (kind) {
	case 1: return @"checkmark";
	case 2: return @"exclamationmark.triangle";
	case 3: return @"xmark.circle";
	default: return @"info.circle";
	}
}

static void fillBox(NSRect r, NSColor *bg, NSColor *border, CGFloat radius) {
	if (bg) {
		[bg setFill];
		if (radius > 0) {
			[[NSBezierPath bezierPathWithRoundedRect:r xRadius:radius yRadius:radius] fill];
		} else {
			NSRectFill(r);
		}
	}
	if (border) {
		NSRect in = NSInsetRect(r, 0.5, 0.5);
		CGFloat rr = radius > 0.5 ? radius - 0.5 : 0;
		NSBezierPath *p = rr > 0 ? [NSBezierPath bezierPathWithRoundedRect:in xRadius:rr yRadius:rr]
		                         : [NSBezierPath bezierPathWithRect:in];
		p.lineWidth = 1;
		[border setStroke];
		[p stroke];
	}
}

// drawLine draws s on one line in r, vertically centred, ellipsized at the end.
static void drawLine(NSString *s, NSFont *f, NSColor *c, NSRect r, NSTextAlignment align) {
	NSMutableParagraphStyle *ps = [[NSMutableParagraphStyle alloc] init];
	ps.lineBreakMode = NSLineBreakByTruncatingTail;
	ps.alignment = align;
	CGFloat lh = lineHeight(f);
	NSRect line = NSMakeRect(r.origin.x, r.origin.y + floor((r.size.height - lh) / 2), r.size.width, lh);
	[s drawWithRect:line
	        options:NSStringDrawingUsesLineFragmentOrigin | NSStringDrawingTruncatesLastVisibleLine
	     attributes:@{NSFontAttributeName : f, NSForegroundColorAttributeName : c, NSParagraphStyleAttributeName : ps}];
}

@implementation ToastView {
	NSTrackingArea *track;
}

- (BOOL)isFlipped { return YES; }
- (BOOL)acceptsFirstMouse:(NSEvent *)e { return YES; }
- (BOOL)mouseDownCanMoveWindow { return NO; }

- (void)updateTrackingAreas {
	[super updateTrackingAreas];
	if (!track) {
		// ActiveAlways: the app that posted the card is usually not the active one.
		track = [[NSTrackingArea alloc] initWithRect:NSZeroRect
		                                     options:NSTrackingMouseEnteredAndExited | NSTrackingMouseMoved |
		                                             NSTrackingActiveAlways | NSTrackingInVisibleRect
		                                       owner:self
		                                    userInfo:nil];
		[self addTrackingArea:track];
	}
}

- (void)send:(int)ev event:(NSEvent *)e {
	NSPoint p = [self convertPoint:e.locationInWindow fromView:nil];
	goToastMouse(self.key, ev, p.x, p.y);
}
- (void)mouseEntered:(NSEvent *)e { [self send:toastMove event:e]; }
- (void)mouseMoved:(NSEvent *)e { [self send:toastMove event:e]; }
- (void)mouseExited:(NSEvent *)e { [self send:toastLeave event:e]; }
- (void)mouseDown:(NSEvent *)e {
	// Control-click is the Mac's right click.
	self.ctrlDown = (e.modifierFlags & NSEventModifierFlagControl) != 0;
	if (!self.ctrlDown) {
		[self send:toastDown event:e];
	}
}
- (void)mouseUp:(NSEvent *)e {
	if (self.ctrlDown) {
		self.ctrlDown = 0;
		[self send:toastRightUp event:e];
		return;
	}
	[self send:toastUp event:e];
}
- (void)rightMouseDown:(NSEvent *)e {} // no context menu
- (void)rightMouseUp:(NSEvent *)e { [self send:toastRightUp event:e]; }

- (void)drawRect:(NSRect)dirty {
	ToastColors k = self.colors;
	ToastGeom g = self.geom;
	NSRect all = self.bounds;
	CGFloat w = all.size.width, h = all.size.height;
	NSColor *accent = rgb(k.accent);

	// The layer's corner mask rounds the card; the stripe sits under the
	// rounded border so the curve stays clean.
	[rgb(k.bg) setFill];
	NSRectFill(all);
	[accent setFill];
	NSRectFill(NSMakeRect(0, 0, g.stripe, h));
	fillBox(all, nil, rgb(k.border), self.radius);

	CGFloat top = g.padTop;
	if (@available(macOS 11.0, *)) {
		NSImage *sym = [NSImage imageWithSystemSymbolName:symbolFor(self.kind) accessibilityDescription:nil];
		NSImageSymbolConfiguration *sc = [NSImageSymbolConfiguration configurationWithPointSize:g.iconSize * 0.9
		                                                                                 weight:NSFontWeightMedium];
		sym = [sym imageWithSymbolConfiguration:sc];
		if (sym) {
			NSSize s = sym.size;
			NSRect r = NSMakeRect(g.iconLeft + floor((g.iconSize - s.width) / 2), top + (g.titleH - s.height) / 2,
			                      s.width, s.height);
			[tinted(sym, accent) drawInRect:r fromRect:NSZeroRect operation:NSCompositingOperationSourceOver
			                       fraction:1 respectFlipped:YES hints:nil];
		}
	}

	drawLine(self.title ?: @"", font(g.titleSize, YES), rgb(k.fg),
	         NSMakeRect(g.textLeft, top, w - g.titleRight - g.textLeft, g.titleH), NSTextAlignmentLeft);

	if (self.lines > 0 && self.body.length) {
		NSFont *f = font(g.bodySize, NO);
		NSMutableParagraphStyle *ps = [[NSMutableParagraphStyle alloc] init];
		ps.lineBreakMode = NSLineBreakByWordWrapping;
		CGFloat bt = top + g.titleH + g.bodyGap;
		NSRect r = NSMakeRect(g.textLeft, bt, w - g.textRight - g.textLeft, self.lines * lineHeight(f) + 1);
		[self.body drawWithRect:r
		                options:NSStringDrawingUsesLineFragmentOrigin | NSStringDrawingTruncatesLastVisibleLine
		             attributes:@{NSFontAttributeName : f, NSForegroundColorAttributeName : rgb(k.muted),
		                          NSParagraphStyleAttributeName : ps}];
	}

	// Inner pieces (the x's hover fill, buttons) get a smaller radius.
	CGFloat inner = self.radius * 0.6;
	NSColor *xc = rgb(k.faint);
	if (self.hot == partClose) {
		fillBox(self.closeR, rgb(k.hover), nil, inner);
		xc = rgb(k.fg);
	} else if (self.hovered) {
		xc = rgb(k.muted);
	}
	NSPoint c = NSMakePoint(NSMidX(self.closeR), NSMidY(self.closeR));
	CGFloat a = 4;
	NSBezierPath *x = [NSBezierPath bezierPath];
	[x moveToPoint:NSMakePoint(c.x - a, c.y - a)];
	[x lineToPoint:NSMakePoint(c.x + a, c.y + a)];
	[x moveToPoint:NSMakePoint(c.x - a, c.y + a)];
	[x lineToPoint:NSMakePoint(c.x + a, c.y - a)];
	x.lineWidth = 1.2;
	x.lineCapStyle = NSLineCapStyleRound;
	[xc setStroke];
	[x stroke];

	for (NSUInteger i = 0; i < self.labels.count && i < 2; i++) {
		NSRect b = i == 0 ? self.b0 : self.b1;
		BOOL hot = self.hot == (int)i;
		NSColor *bg = hot ? rgb(k.hover) : rgb(k.button), *fg = rgb(k.fg), *border = rgb(k.border);
		if (i == 0) { // primary
			bg = hot ? rgb(k.primaryHover) : rgb(k.primary);
			fg = rgb(k.onPrimary);
			border = nil;
		}
		fillBox(b, bg, border, inner);
		drawLine(self.labels[i], font(g.buttonSize, YES), fg, NSInsetRect(b, 4, 0), NSTextAlignmentCenter);
	}
}
@end

// Main-thread state.
static NSMutableDictionary<NSNumber *, ToastPanel *> *panels;
static NSTimer *ticker;

static ToastPanel *panelFor(uint64_t key) { return panels[@(key)]; }
static ToastView *viewFor(uint64_t key) { return (ToastView *)panelFor(key).contentView; }

void toast_wake(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		goToastSync();
	});
}

void toast_setup(void) {
	static dispatch_once_t once;
	dispatch_once(&once, ^{
		panels = [NSMutableDictionary dictionary];
		[[NSNotificationCenter defaultCenter] addObserverForName:NSApplicationDidChangeScreenParametersNotification
		                                                  object:nil
		                                                   queue:[NSOperationQueue mainQueue]
		                                              usingBlock:^(NSNotification *n) {
			                                              goToastRehome();
		                                              }];
	});
}

void toast_timer(int on) {
	if (on && !ticker) {
		ticker = [NSTimer timerWithTimeInterval:0.030 repeats:YES block:^(NSTimer *t) {
			goToastTick();
		}];
		// Common modes: the cards keep counting down while the app tracks a
		// menu or a window resize.
		[[NSRunLoop mainRunLoop] addTimer:ticker forMode:NSRunLoopCommonModes];
	} else if (!on && ticker) {
		[ticker invalidate];
		ticker = nil;
	}
}

static NSScreen *mouseScreen(void) {
	NSPoint m = [NSEvent mouseLocation];
	for (NSScreen *s in [NSScreen screens]) {
		if (NSMouseInRect(m, s.frame, NO)) {
			return s;
		}
	}
	return [NSScreen mainScreen] ?: [NSScreen screens].firstObject;
}

ToastRect toast_work_area(int active, ToastRect old) {
	NSScreen *scr = nil;
	if (!active) { // the screen the stack is on, if it is still there
		CGFloat H = primaryHeight();
		NSPoint c = NSMakePoint((old.l + old.r) / 2, H - (old.t + old.b) / 2);
		for (NSScreen *s in [NSScreen screens]) {
			if (NSPointInRect(c, s.frame)) {
				scr = s;
				break;
			}
		}
	}
	if (!scr) {
		scr = mouseScreen();
	}
	if (!scr) {
		return (ToastRect){0, 0, 1280, 800};
	}
	// visibleFrame leaves out the menu bar and the Dock.
	return topDown(scr.visibleFrame);
}

int toast_body_lines(const char *body, double width, double size) {
	NSString *s = [NSString stringWithUTF8String:body];
	if (!s.length) {
		return 0;
	}
	NSFont *f = font(size, NO);
	NSRect r = [s boundingRectWithSize:NSMakeSize(width, CGFLOAT_MAX)
	                           options:NSStringDrawingUsesLineFragmentOrigin
	                        attributes:@{NSFontAttributeName : f}];
	CGFloat one = lineHeight(f);
	return r.size.height > one * 1.5 ? 2 : 1;
}

double toast_text_width(const char *s, double size) {
	NSString *str = [NSString stringWithUTF8String:s];
	return ceil([str sizeWithAttributes:@{NSFontAttributeName : font(size, YES)}].width);
}

int toast_card_create(uint64_t key, const char *title, int captureExclude, double w, double h) {
	ToastPanel *p = [[ToastPanel alloc] initWithContentRect:NSMakeRect(0, 0, w, h)
	                                              styleMask:NSWindowStyleMaskBorderless | NSWindowStyleMaskNonactivatingPanel
	                                                backing:NSBackingStoreBuffered
	                                                  defer:NO];
	if (!p) {
		return 0;
	}
	p.title = [NSString stringWithUTF8String:title];
	p.releasedWhenClosed = NO;
	p.floatingPanel = YES;
	p.becomesKeyOnlyIfNeeded = YES;
	p.hidesOnDeactivate = NO; // NSPanel's default would hide the cards whenever the app isn't frontmost
	p.level = NSStatusWindowLevel;
	p.collectionBehavior = NSWindowCollectionBehaviorCanJoinAllSpaces | NSWindowCollectionBehaviorFullScreenAuxiliary |
	                       NSWindowCollectionBehaviorStationary | NSWindowCollectionBehaviorIgnoresCycle;
	p.opaque = NO;
	p.backgroundColor = [NSColor clearColor];
	p.hasShadow = YES;
	p.movable = NO;
	p.animationBehavior = NSWindowAnimationBehaviorNone;
	p.acceptsMouseMovedEvents = YES;
	p.alphaValue = 0; // fades in from nothing
	if (captureExclude) {
		p.sharingType = NSWindowSharingNone;
	}
	ToastView *v = [[ToastView alloc] initWithFrame:NSMakeRect(0, 0, w, h)];
	v.key = key;
	v.hot = -3;
	v.wantsLayer = YES;
	p.contentView = v;
	panels[@(key)] = p;
	return 1;
}

void toast_card_content(uint64_t key, const char *title, const char *body, int kind, int lines,
	const char *label0, const char *label1, int nlabels, ToastRect close, ToastRect b0, ToastRect b1,
	double radius, ToastColors colors, ToastGeom geom) {
	ToastView *v = viewFor(key);
	if (!v) {
		return;
	}
	v.title = [NSString stringWithUTF8String:title];
	v.body = [NSString stringWithUTF8String:body];
	v.kind = kind;
	v.lines = lines;
	NSMutableArray *labels = [NSMutableArray array];
	if (nlabels > 0) {
		[labels addObject:[NSString stringWithUTF8String:label0]];
	}
	if (nlabels > 1) {
		[labels addObject:[NSString stringWithUTF8String:label1]];
	}
	v.labels = labels;
	v.closeR = fromRect(close);
	v.b0 = fromRect(b0);
	v.b1 = fromRect(b1);
	v.radius = radius;
	v.colors = colors;
	v.geom = geom;
	v.layer.cornerRadius = radius;
	v.layer.masksToBounds = radius > 0;
	v.needsDisplay = YES;
}

void toast_card_state(uint64_t key, int hot, int hovered, int clickable) {
	ToastView *v = viewFor(key);
	if (!v) {
		return;
	}
	if (v.hot != hot || v.hovered != hovered) {
		v.hot = hot;
		v.hovered = hovered;
		v.needsDisplay = YES;
	}
	v.clickable = clickable;
	if (hovered) {
		BOOL hand = hot >= 0 || hot == partClose || (hot == partBody && clickable);
		[(hand ? [NSCursor pointingHandCursor] : [NSCursor arrowCursor]) set];
	}
}

void toast_card_frame(uint64_t key, double x, double y, double w, double h, int raise) {
	ToastPanel *p = panelFor(key);
	if (!p) {
		return;
	}
	NSRect f = NSMakeRect(x, primaryHeight() - y - h, w, h);
	if (!NSEqualRects(f, p.frame)) {
		BOOL resized = !NSEqualSizes(f.size, p.frame.size);
		[p setFrame:f display:YES];
		if (resized) {
			[p invalidateShadow];
		}
	}
	if (raise && p.visible) {
		[p orderFrontRegardless];
	}
}

void toast_card_alpha(uint64_t key, double a) { panelFor(key).alphaValue = a; }

void toast_card_show(uint64_t key) {
	ToastPanel *p = panelFor(key);
	[p orderFrontRegardless]; // shows it without activating the app or making it key
	[p invalidateShadow];
}

void toast_card_through(uint64_t key, int on) { panelFor(key).ignoresMouseEvents = on != 0; }

void toast_card_destroy(uint64_t key) {
	ToastPanel *p = panelFor(key);
	if (!p) {
		return;
	}
	// orderOut, not close: an app that quits after its last window closes
	// mustn't count a card.
	[p orderOut:nil];
	[panels removeObjectForKey:@(key)];
}

// Sounds play on their own serial queue, off the main thread; the players
// are held until they finish.
void toast_play(const void *wav, int n) {
	static dispatch_queue_t q;
	static NSMutableArray<AVAudioPlayer *> *playing;
	static dispatch_once_t once;
	dispatch_once(&once, ^{
		q = dispatch_queue_create("go-toast.sound", DISPATCH_QUEUE_SERIAL);
		playing = [NSMutableArray array];
	});
	NSData *d = [NSData dataWithBytes:wav length:n]; // a copy: Go may reuse its buffer
	dispatch_async(q, ^{
		@autoreleasepool {
			NSIndexSet *done = [playing indexesOfObjectsPassingTest:^BOOL(AVAudioPlayer *p, NSUInteger i, BOOL *stop) {
				return !p.playing;
			}];
			[playing removeObjectsAtIndexes:done];
			NSError *err = nil;
			AVAudioPlayer *p = [[AVAudioPlayer alloc] initWithData:d error:&err];
			if (!p) {
				return;
			}
			[playing addObject:p];
			if (playing.count > 8) {
				[playing[0] stop];
				[playing removeObjectAtIndex:0];
			}
			[p play];
		}
	});
}

int toast_main_thread(void) { return [NSThread isMainThread] ? 1 : 0; }

int toast_app_running(void) { return NSApp != nil && [NSApp isRunning] ? 1 : 0; }

void toast_main_run(void) {
	@autoreleasepool {
		[NSApplication sharedApplication];
		// An unbundled binary starts Prohibited, which may not show windows.
		// Accessory shows them without a Dock icon or a menu bar.
		if (NSApp.activationPolicy == NSApplicationActivationPolicyProhibited) {
			[NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
		}
		[NSApp run];
	}
}

void toast_main_stop(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		[NSApp stop:nil];
		// stop: takes effect after the next event; post one so it does now.
		NSEvent *e = [NSEvent otherEventWithType:NSEventTypeApplicationDefined
		                                location:NSZeroPoint
		                           modifierFlags:0
		                               timestamp:0
		                            windowNumber:0
		                                 context:nil
		                                 subtype:0
		                                   data1:0
		                                   data2:0];
		[NSApp postEvent:e atStart:YES];
	});
}

// Tests.

ToastPanelInfo toast_test_info(uint64_t key) {
	__block ToastPanelInfo out = {0};
	dispatch_sync(dispatch_get_main_queue(), ^{
		out.appActive = NSApp.active;
		out.appHasKey = NSApp.keyWindow != nil;
		ToastPanel *p = panelFor(key);
		if (!p) {
			return;
		}
		out.exists = 1;
		out.visible = p.visible;
		out.key = p.keyWindow;
		out.main = p.mainWindow;
		out.ignoresMouse = p.ignoresMouseEvents;
		out.sharingNone = p.sharingType == NSWindowSharingNone;
		out.level = (int)p.level;
		out.alpha = p.alphaValue;
		out.frame = topDown(p.frame);
		NSScreen *s = p.screen;
		if (s) {
			out.work = topDown(s.visibleFrame);
			out.onScreen = NSContainsRect(s.visibleFrame, p.frame);
		}
	});
	return out;
}

// toast_test_mouse sends a synthetic mouse event at (x, y) in the card's
// top-down view coordinates through the window, as a real click would arrive.
void toast_test_mouse(uint64_t key, int ev, double x, double y) {
	dispatch_sync(dispatch_get_main_queue(), ^{
		ToastPanel *p = panelFor(key);
		if (!p) {
			return;
		}
		ToastView *v = (ToastView *)p.contentView;
		NSPoint inWin = [v convertPoint:NSMakePoint(x, y) toView:nil];
		NSEventType t;
		switch (ev) {
		case toastDown: t = NSEventTypeLeftMouseDown; break;
		case toastUp: t = NSEventTypeLeftMouseUp; break;
		case toastRightUp: t = NSEventTypeRightMouseUp; break;
		default: t = NSEventTypeMouseMoved; break;
		}
		NSEvent *e = [NSEvent mouseEventWithType:t
		                                location:inWin
		                           modifierFlags:0
		                               timestamp:[[NSProcessInfo processInfo] systemUptime]
		                            windowNumber:p.windowNumber
		                                 context:nil
		                             eventNumber:0
		                              clickCount:1
		                                pressure:t == NSEventTypeLeftMouseDown ? 1 : 0];
		if (t == NSEventTypeMouseMoved) {
			[v mouseMoved:e]; // tracking-area events don't route through sendEvent:
		} else {
			[p sendEvent:e];
		}
	});
}

int toast_test_frontmost_pid(void) {
	return [[NSWorkspace sharedWorkspace] frontmostApplication].processIdentifier;
}
