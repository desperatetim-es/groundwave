groundwave {{VERSION}} for Linux ({{ARCH}})
=========================================

groundwave is an offline amateur-radio reference: your own library of manuals,
antenna tools, an offline US callsign database, a logbook, and a GPS clock.
It is one program with nothing to install.

RUN IT
------
Open a terminal in this folder and run:

    ./groundwave

It prints a web address (normally http://127.0.0.1:47321) and opens it in your
default browser. If no browser opens (a headless or minimal system), copy the
address into a browser yourself. Press Ctrl+C in the terminal, or use Quit in
the app, to stop it. Closing the browser tab does not stop it.

If it says "Permission denied", the executable bit was lost; fix it with:

    chmod +x groundwave

Wrong download? Run `uname -m` and pick the file that matches:
x86_64 = normal PC, aarch64 = 64-bit Raspberry Pi, armv7l = 32-bit Raspberry Pi.

USER GUIDE
----------
A copy of the user guide is in the app's Library (open it from the Library page).
It works with no internet.

YOUR DATA
---------
Your library, logbook and callsign database live in ~/Documents/Groundwave
(or ~/Groundwave if there is no Documents folder). Use --data <folder> or the
GROUNDWAVE_HOME environment variable to put them somewhere else. Nothing is
sent anywhere unless you press a button that says so.

GPS RECEIVER (USB)
------------------
Reading a serial device needs permission. The program warns at startup if you
lack it. The usual fix (then log out and back in):

    sudo usermod -aG dialout $USER        (Debian, Ubuntu, Fedora, Raspberry Pi OS)
    sudo usermod -aG uucp $USER           (Arch, and some others)

If a USB GPS or radio disappears the moment you plug it in, ModemManager may be
grabbing it. Stop it with:  sudo systemctl stop ModemManager

OPTIONS
-------
    --data <folder>   where your data lives
    --port <n>        port to listen on (default 47321; 0 = any free port)
    --no-browser      do not open a browser
    --version         print the version

LICENSE
-------
groundwave is free software under the GNU GPL v3 (see LICENSE). Source code:
https://github.com/kd9psm/groundwave
