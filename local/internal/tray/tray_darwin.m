#import <Cocoa/Cocoa.h>
#import "tray_darwin.h"

extern void goOnTurnOff(void);
extern void goOnTurnOn(void);
extern void goOnOpenWebsite(void);
extern void goOnQuit(void);

@interface TrayHelper : NSObject
- (void)onTurnOff:(id)sender;
- (void)onTurnOn:(id)sender;
- (void)onOpenWebsite:(id)sender;
- (void)onQuit:(id)sender;
@end

static NSStatusItem *statusItem = nil;
static NSMenu *trayMenu = nil;
static NSMenuItem *statusMenuItem = nil;
static NSMenuItem *toggleMenuItem = nil;
static TrayHelper *trayHelper = nil;

@implementation TrayHelper
- (void)onTurnOff:(id)sender {
    goOnTurnOff();
}
- (void)onTurnOn:(id)sender {
    goOnTurnOn();
}
- (void)onOpenWebsite:(id)sender {
    goOnOpenWebsite();
}
- (void)onQuit:(id)sender {
    goOnQuit();
}
@end

void initTray(const char* title, const char* version) {
    [NSApplication sharedApplication];
    [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];

    void (^setupBlock)(void) = ^{
        if (trayHelper == nil) {
            trayHelper = [[TrayHelper alloc] init];
        }

        if (statusItem == nil) {
            statusItem = [[NSStatusBar systemStatusBar] statusItemWithLength:NSSquareStatusItemLength];
            
            NSImage *icon = [NSImage imageWithSize:NSMakeSize(18, 18) flipped:NO drawingHandler:^BOOL(NSRect dstRect) {
                [[NSColor headerTextColor] setFill];
                NSBezierPath *path = [NSBezierPath bezierPathWithOvalInRect:NSMakeRect(4, 4, 10, 10)];
                [path fill];
                return YES;
            }];
            [icon setTemplate:YES];
            [statusItem.button setImage:icon];
            [statusItem.button setToolTip:@"YouPiper Helper"];
        }

        if (trayMenu == nil) {
            trayMenu = [[NSMenu alloc] init];

            // Header
            NSMenuItem *headerItem = [[NSMenuItem alloc] initWithTitle:@"YouPiper" action:nil keyEquivalent:@""];
            [headerItem setEnabled:NO];
            NSFont *boldFont = [NSFont boldSystemFontOfSize:[NSFont systemFontSize]];
            NSDictionary *attributes = @{ NSFontAttributeName: boldFont };
            NSAttributedString *attrTitle = [[NSAttributedString alloc] initWithString:@"YouPiper" attributes:attributes];
            [headerItem setAttributedTitle:attrTitle];
            [trayMenu addItem:headerItem];

            [trayMenu addItem:[NSMenuItem separatorItem]];

            // Status Indicator
            statusMenuItem = [[NSMenuItem alloc] initWithTitle:@"● Helper Running" action:nil keyEquivalent:@""];
            [statusMenuItem setEnabled:NO];
            [trayMenu addItem:statusMenuItem];

            [trayMenu addItem:[NSMenuItem separatorItem]];

            // Toggle item (Turn Off / Turn On)
            toggleMenuItem = [[NSMenuItem alloc] initWithTitle:@"Turn Off Helper" action:@selector(onTurnOff:) keyEquivalent:@""];
            [toggleMenuItem setTarget:trayHelper];
            [trayMenu addItem:toggleMenuItem];

            // Open YouPiper
            NSMenuItem *openItem = [[NSMenuItem alloc] initWithTitle:@"Open YouPiper" action:@selector(onOpenWebsite:) keyEquivalent:@""];
            [openItem setTarget:trayHelper];
            [trayMenu addItem:openItem];

            [trayMenu addItem:[NSMenuItem separatorItem]];

            // Quit item
            NSMenuItem *quitItem = [[NSMenuItem alloc] initWithTitle:@"Quit" action:@selector(onQuit:) keyEquivalent:@"q"];
            [quitItem setTarget:trayHelper];
            [trayMenu addItem:quitItem];

            [statusItem setMenu:trayMenu];
        }
    };

    if ([NSThread isMainThread]) {
        setupBlock();
    } else {
        dispatch_async(dispatch_get_main_queue(), setupBlock);
    }
}

void setTrayStatus(int running) {
    dispatch_async(dispatch_get_main_queue(), ^{
        if (statusMenuItem && toggleMenuItem) {
            if (running) {
                [statusMenuItem setTitle:@"● Helper Running"];
                [toggleMenuItem setTitle:@"Turn Off Helper"];
                [toggleMenuItem setAction:@selector(onTurnOff:)];
            } else {
                [statusMenuItem setTitle:@"○ Helper Off"];
                [toggleMenuItem setTitle:@"Turn On Helper"];
                [toggleMenuItem setAction:@selector(onTurnOn:)];
            }
        }
    });
}

void runTray(void) {
    @autoreleasepool {
        [NSApplication sharedApplication];
        [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
        [NSApp run];
    }
}

void stopTray(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        [NSApp terminate:nil];
    });
}
