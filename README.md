<p align="center"><img src="app/build/icon.png" alt="" width="112"></p>

<h1 align="center">Eris</h1>

<p align="center"><b>A Discord-style interface, TeamSpeak-style rooms, and Parsec-style controller sharing,<br>on a server you host yourself.</b></p>

## What that means

- **Rooms, like TeamSpeak.** Everything happens in rooms. Each room has its own text
  chat, voice and cameras; there are no direct calls, because a server is already a
  private community. Rooms can be private, by password or by a list of members.
- **A familiar look, like Discord.** Your servers down the left, the server's rooms, the
  chat in the middle, members on the right. Pictures show in the chat, files can be
  dragged in, messages can be edited and deleted.
- **Play together, like Parsec.** Stream your game and let friends join with their own
  controllers, at low latency. Eris adjusts the picture to each player's connection and PC
  as it goes. In our tests on one PC, the stream ran at 60 fps with about 28 ms from the
  host's screen to the player's.
- **Your own server.** One person hosts it, on their own PC. The connection is encrypted
  with the server's own certificate, the history and files stay on that PC for as long as
  its host chooses, and the server updates itself.

*Not affiliated with Discord, TeamSpeak or Parsec.*

> **Early days.** Eris is at version 0.1. Text chat, files, private rooms, voice, cameras,
> hosting and streaming work; watching streams inside the app and push-to-talk come next. Going live is
> Linux-only for now (Windows can join and play).

---

## Installing

Download the latest version from the
[releases page](https://github.com/freethought78/Eris/releases/latest).

- **Windows:** run `Eris-Setup-<version>.exe`. It installs for you only, no admin needed,
  with Start menu and desktop icons. It isn't code-signed yet, so Windows asks first:
  choose *More info*, then *Run anyway*.
- **Linux:** download `Eris-<version>.AppImage` and put it wherever you keep programs.
  Make it runnable (right-click, *Properties*, *Executable as Program*, or
  `chmod +x Eris-*.AppImage`) and open it. It adds itself to your app menu and puts an
  icon on the desktop (delete the icon if you don't want it; it won't come back).

**Updates** are offered by the app itself: when a server you connect to has been updated,
or when you press *Check for updates*. It downloads the update and restarts into it when
you say so.

## Joining a server

1. Get the **join link** from the server's host. It looks like
   `eris://their.address:7300#…`. Give the app the whole link: the part after `#`
   proves it's really their server.
2. In Eris, press **+** at the top left, paste the link, and enter the name others will
   see (and the server's password, if it has one).

The first time you open a private room, Eris asks for its password; you only give it once.

## Chatting

- **Enter** sends, **Shift+Enter** starts a new line, **Up** edits your last message.
- Attach files with the **+** button, by dragging them in, or by pasting. Pictures show in
  the chat (click one to see it full size); other files get a *Save* button.
- Links open in your browser. A dot next to a room means there's something new in it.

## Talking

- **Join a room's voice** by double-clicking the room, or with **Join voice** at the top of
  its chat (or the speaker that appears when you point at a room). You can read another
  room's chat while you talk in one.
- Who's in each room's voice is listed under the room; a green light shows who's speaking.
- The bar at the bottom of the rooms list has **mute** (🎤), **deafen** (🎧: hear nobody,
  and mute), **camera** (📷) and **leave** (✕). Cameras show above the room's chat.
- Click someone's name under the room to change how loud they are (just for you).
- **⚙** at the bottom right of the rooms list picks your microphone, speakers and camera.
  Echo cancellation and noise suppression are always on.

## Hosting your own server

The server comes with the app (Linux for now).

1. Press **⌂** (*Host*) at the bottom of the server list, then **Start server**. The server
   opens in its own small terminal window, which shows who's online and stays open as long
   as it runs. It keeps running when you close Eris; close its window (or press *Stop
   server*) to stop it.
2. Copy the **join link** from Host and give it to your friends. The first time, press
   **Join it** yourself: the **owner code** is filled in, and whoever joins with it first
   becomes the owner.
3. Your friends need to reach your PC: forward **TCP 7300** and, for voice and cameras,
   **UDP 7301** on your router to it (and, for streaming games, **UDP 7401**), or put it
   in your router's DMZ.

As owner, the **Server** button at the bottom of the rooms list gives you the server's
status, log, *Check for updates*, *Restart* and *Stop* from any PC. The **+** next to
*Rooms* adds a room; the gear beside a room changes it or makes it private.

**Updates:** the server checks for a new version every hour and installs it as soon as
nobody is streaming; members' apps reconnect by themselves. *Check for updates* in Host
does it straight away, and updates the app too.

**Settings** live with the server's data (`~/.config/Eris-server` on Linux):
`host.json` holds the data folder, the port and the address in the join link;
`server-data/settings.json` holds the server's name, an optional password, and how long
messages and files are kept (30 days by default), the largest file allowed (100 MB) and
the total space for files (10 GB).

## Playing together

On Linux, press **Go Live** to stream your main screen. Members see *LIVE* next to your
name and press **Join**: the game opens in a window on their PC, and their controller
works in your game. *Stream test* runs a 60-second test with a member and keeps both
sides' measurements (under *Stream tests*).

---

## For developers

| Folder | What's there |
|---|---|
| `app/` | The Electron app (Svelte). See [app/README.md](app/README.md). |
| `server/` | eris-server, in Go. See [server/README.md](server/README.md). |
| `helper/` | The streaming helper (capture, encoding, the player, controllers), in Go. See [helper/README.md](helper/README.md) for the measurements. |
| `tools/` | `release.sh` builds and publishes a release; `dev.sh` runs this folder's code next to an installed Eris. |

[DESIGN.md](DESIGN.md) is the design, and the order things are built in.

- **Try changes:** `tools/dev.sh` builds the code in this folder and runs it as a separate
  dev app with its own dev server (a copy of your real server's data), without touching
  your installed Eris. A dev server never installs a release over itself.
- **Tests:** `go test ./...` in `server/` and `helper/`; `app/test-e2e.sh` runs a server
  and two apps end to end on one PC (chat, files and a stream test).
