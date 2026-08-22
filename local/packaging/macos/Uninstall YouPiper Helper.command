#!/bin/bash
#
# Removes YouPiper Helper from this Mac.
#
# Your downloaded files are never touched — only the Helper itself, its login
# item and its log file are removed.
set -u

LABEL="com.youpiper.helper"
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
APP_NAME="YouPiper Helper.app"

echo "Removing YouPiper Helper..."

# Stop it and remove the login item, so nothing is left starting at login.
launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null
launchctl unload -w "$PLIST" 2>/dev/null
rm -f "$PLIST"

# Stop any copy still running (for example one started by double-clicking).
pkill -f "/$APP_NAME/Contents/MacOS/youpiper-helper" 2>/dev/null

removed=0
for dir in "/Applications" "$HOME/Applications" "$HOME/Downloads" "$HOME/Desktop"; do
	if [ -d "$dir/$APP_NAME" ]; then
		rm -rf "$dir/$APP_NAME" && removed=1
		echo "  Removed $dir/$APP_NAME"
	fi
done

rm -rf "$HOME/Library/Logs/YouPiper"

if [ "$removed" -eq 0 ]; then
	echo "  The application itself was not in a standard location."
	echo "  Drag YouPiper Helper to the Trash to finish."
fi

echo
echo "Done. YouPiper Helper will no longer start automatically."
echo "Your downloads in your Downloads folder were not touched."
echo
echo "You can close this window."
