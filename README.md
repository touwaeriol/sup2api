# moderation 0.11.1 trace reason screenshots

Mock console (next/web `vite --mode mock`), record detail dialog; the stage-2
DeepSeek outcome is injected with a 777-character, 7-line reason (with a long
unbroken token) and refused images (HTTP 413).

- 0-before-1024.png: before (0.11.0 TraceView), 1024 wide: the reason column is cut to "1…"
- 1-desktop-1440-dialog.png: after, 1440 wide
- 2a-tablet-1024-dialog.png: after, 1024 wide (table layout in the dialog)
- 2b-dialog-760.png: after, 760 wide (card layout)
- 3-phone-390.png: after, phone 390 wide, scrolled to the reason
- 4-dark-1440.png: after, dark mode
- 5-blocks-1440.png: blocked users, a ban reason opened under its row
