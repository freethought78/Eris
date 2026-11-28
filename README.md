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

> **Early days.** Eris is at version 0.2. Text chat, files, private rooms, voice, cameras,
> screen sharing, hosting and Play Together work; push-to-talk comes later. Going live for
> Play Together is Linux-only for now (Windows can share its screen, join and play).

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
- Attach files with the **+** button, by dragging them in, or by pasting: they're sent
  straight away (with anything you've typed). Pictures show in the chat (click one to see
  it full size), audio and videos play in it, and other files get a *Save* button.
- Links open in your browser. A dot next to a room means there's something new in it.

## Talking, cameras and sharing your screen

- **Join a room's voice** by double-clicking the room, or with **Join voice** at the top of
  its chat (or the speaker that appears when you point at a room). You can read another
  room's chat while you talk in one.
- Who's in each room's voice is listed under the room; a green light shows who's speaking.
- The panel at the bottom of the rooms list has **mute**, **deafen** (hear nobody, and
  mute), **camera**, **share your screen** and **leave** (point at a button for its name).
  Cameras and shares show above the room's chat: click one to make it big, drag the bar
  under it to resize, and double-click (or its button) for fullscreen. The whole picture
  always shows, with black bars where its shape differs from the space. Your own share
  shows too, as others see it.
- **Sharing your screen:** pick a whole screen or one window (on Windows, tick *Share
  sound too* to include its sound; Eris leaves the call's voices out of it, so nobody hears
  themselves). Others in the room see a card saying you're sharing,
  and only those who press **Watch** receive it, so nobody's connection carries a share
  they aren't watching. Watching opens it big above the chat.
- Click someone's name under the room to change how loud they are, from silent to 300%
  (just for you; a limiter keeps loud moments from crackling).

## Settings

**⚙** (bottom left, or in the panel under the rooms) opens Settings:

- **Voice & video:** your microphone, speakers and camera. Echo cancellation and noise
  suppression are always on.
- **Your server (this PC):** host a server on this PC (see below).
- **Server: (name):** for a server you own, from any PC: its status, log, icon, *Check for
  updates*, *Restart* and *Stop*.
- **About Eris:** your version and updates, and **diagnostics**: Eris sends the owner of each
  server you're on what helps them fix problems (this app's log, connection stats while in
  voice, Play Together sessions' logs; never your messages), kept for 3 days. You can turn
  it off there.

## Hosting your own server

The server comes with the app (Linux for now).

1. Open **Settings**, **Your server (this PC)**, and press **Start server**. The server opens
   in its own small terminal window, which shows who's online and stays open as long as it
   runs. It keeps running when you close Eris; close its window (or press *Stop server*) to
   stop it. If it's stopped, its page in Eris says so and offers **Start server**.
2. Copy the **join link** there and give it to your friends. The first time, press
   **Join it** yourself: the **owner code** is filled in, and whoever joins with it first
   becomes the owner.
3. Your friends need to reach your PC: forward **TCP 7300** and, for voice, cameras and
   screen sharing, **UDP 7301** on your router to it (and, for Play Together, **UDP 7401**),
   or put it in your router's DMZ.

As owner, **Settings, Server: (name)** also sets the server's **icon**, which everyone sees in
their server list. The **+** next to *Rooms* adds a room; the gear beside a room changes
it or makes it private.

**Updates:** the server checks for a new version every hour and installs it as soon as
nobody is streaming; members' apps reconnect by themselves. *Check for updates* does it
straight away, and updates the app too.

**Its files** live in `~/.config/Eris-server` on Linux: `host.json` holds the data
folder, the port and the address in the join link; `server-data/settings.json` holds the
server's name, an optional password, and how long messages and files are kept (30 days by
default), the largest file allowed (100 MB) and the total space for files (10 GB). Its
logs (its own, LiveKit's and members' diagnostics) are in `server-data/logs`, kept for 3
days.

## Playing together

On Linux, press **🎮** in the panel under the rooms to go live: low-latency streaming of
your main screen, and its sound, for playing together.

- **Players:** members in your room's voice see a card saying you're live and press **Ask
  to play** (or next to your name in the members list). You see who's asking, at the top,
  and choose **Let them play** or **Decline**. Once in, the game opens in a window on their
  PC (F11, Alt+Enter or a double-click for fullscreen), with the game's sound, and their
  controller works in your game. Up to 3 players at once (players 2 to 4; you're player
  1), each with their own controller. **Take controller back**, on your live card, unplugs
  a player's controller and closes their game window; they can ask again.
- **Spectators:** while you're live and in a room's voice, your game is also shared in the
  call, with its sound. Everyone else presses **Watch** on the card, as with any shared
  screen, and nothing is sent until someone does. *Stop sharing* on that card stops it
  (Play Together goes on); sharing a screen or window by hand replaces it.
- **Sound:** players and spectators hear your game and everything else your PC plays,
  except Eris itself: the call's voices are left out, so nobody hears themselves.
- **Mullvad:** if it's installed, Eris always runs your server and Play Together outside
  it, so friends can reach them even if you connect Mullvad later. With another VPN, add
  Eris to its split tunnelling (or turn it off while hosting).

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
