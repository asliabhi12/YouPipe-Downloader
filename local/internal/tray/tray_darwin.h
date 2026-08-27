#ifndef TRAY_DARWIN_H
#define TRAY_DARWIN_H

void initTray(const char* title, const char* version);
void setTrayStatus(int running);
void runTray(void);
void stopTray(void);

#endif
