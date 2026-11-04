# Focus Guard

A focus-enforcement app for Windows 10/11. During the time windows you define it blocks **websites** (in every browser) and **desktop apps**. It can only be switched off with a **passcode your parents set**, and it updates itself from this GitHub repository.

> **Honest summary:** Focus Guard is a strong lock against a *standard Windows account*. It is not unbreakable. Anyone with administrator rights, or physical access to the machine, can get around it. See [Known limitations and bypass routes](#known-limitations-and-bypass-routes) before relying on it.

## How it works

```
Windows service "FocusGuard" (SYSTEM, starts at boot, restarts itself)
 ├─ scheduler        trusted clock + recurring / one-off windows
 ├─ site blocker     hosts file + local DNS sinkhole (via NRPT) + firewall + browser policies
 ├─ app blocker      process scan every second; matches name, folder, original file name, publisher
 ├─ watchdog         every 3 s re-checks and repairs hosts / DNS / firewall / policy changes
 ├─ free time        parent-granted credit a child can spend to pause a window (see below)
 ├─ auth             argon2id passcode hash, 5 wrong tries = 15 minute lockout (survives restarts)
 ├─ updater          signed GitHub Releases, installs only between focus windows, rolls back on failure
 └─ dashboard        http://127.0.0.1:47820 (loopback only)

Tray helper (per user)  tray icon, notifications ("Steam was closed"), opens the dashboard as an app window
```

The service runs as SYSTEM, starts at boot (also in Safe Mode), is restarted by Windows within a second if it ever exits unexpectedly, and its security descriptor gives standard users read-only access, so they cannot stop it from Task Manager, `services.msc` or `sc`. The data directory `C:\ProgramData\FocusGuard` and the install folder are writable only by SYSTEM and Administrators.

- **Websites:** blocked domains (and every subdomain, plus `www`) are routed to a local sinkhole, so they fail in Chrome, Edge, Firefox and anything else that uses Windows DNS. Chrome/Edge/Brave/Firefox get policies that turn off built-in DNS-over-HTTPS and QUIC, and the firewall blocks known public DoH/DoT resolvers. During a window the same browsers are also forced to connect directly, so a proxy or VPN browser extension cannot route around the block. `http://` visits to a blocked site show a friendly "blocked until 9:00 PM" page; `https://` visits fail with a browser error (showing a page there would need a custom root certificate, which this app deliberately does not install).
- **Apps:** a blocked program that starts is closed (with its child processes) within about a second. Anything already running when a window starts is closed. Matching uses the file name **and** the install folder **and** the PE "original file name" **and** the code-signing publisher, so renaming `steam.exe` is not enough.
- **Time:** windows are checked against a trusted clock = PC clock + a monotonic offset, corrected against the `Date` header of three HTTPS hosts. Moving the PC clock forward to skip a window is ignored and logged. Offline, it falls back to the local clock. Midnight crossings, DST changes, sleep/wake and booting mid-window are handled and covered by tests.

## Install

You need an **administrator** to install (once), and the day-to-day account should be a **standard** account (see below).

1. Download `FocusGuard-Setup-<version>.exe` from the [latest release](https://github.com/Azaxek/focus-blocker/releases/latest).
2. Run it as an administrator (accept the UAC prompt). It installs to `C:\Program Files\FocusGuard`, registers the service, adds a desktop shortcut and starts the tray helper at logon.
3. A "Focus Guard" window opens for the **parent setup**.

### Parent passcode setup

A parent chooses the passcode on first run (at least 6 characters, entered twice). It is stored only as a salted argon2id hash in `C:\ProgramData\FocusGuard\auth.json`, readable only by SYSTEM and Administrators, and is never logged or displayed.

The passcode is required to: end a window early, change schedules or block lists, view the activity log, change the passcode, change update settings, stop the service, and uninstall or reinstall. Five wrong attempts lock the prompt for 15 minutes, even across restarts and reboots. **The passcode cannot be recovered**; write it down somewhere safe.

### Standard-account setup (important)

The lock is only as strong as the account it runs under. Windows lets an administrator stop services, edit the hosts file and uninstall anything.

1. Make sure a parent has an **administrator** account with a strong password nobody else knows.
2. Make the account used for daily life a **standard user**: *Settings > Accounts > Other users*, pick the account, *Change account type > Standard User* (do this from the parent's admin account).
3. Check from the daily account: open PowerShell and run `whoami /groups`. The line `BUILTIN\Administrators` must say **Group used for deny only** (or not be listed at all).
4. Keep User Account Control at its default (search the Start menu for "Change User Account Control settings") so installs ask for the parent's password.

## Using it

- **Dashboard:** big *Blocking* / *Free time* status, countdown, today's timeline, focus streak and hours.
- **Ending early and turning it back on:** a parent ends a window with *End early* (passcode; a locked dashboard signs in with that same entry). While that window's time is still running, the Today page shows *Turn "School Nights" back on*. It needs **no passcode**, because it can only make things stricter. The early end stays on the day's record, so the streak is not given back. The activity log shows both steps.
- **Schedule:** click and drag on a day to add a window; click a block to edit it. Windows can repeat weekly, be one-day-only, cross midnight, overlap, and use the global lists or their own.
- **Block lists:** category presets (Video, Social, Streaming, Chat, Gaming), search, add/remove sites; apps from presets, from a list of running programs, or by file name.
- **Parent area:** activity log (window start/end, blocked attempts, tamper events, free time, update history), free time, update settings, time zone, change passcode, stop the service.
- **Heads-up notifications:** a notice 5 minutes before a window starts and 2 minutes before earned free time ends.
- **Stay hard (motivation):** short lines in the style of David Goggins appear where the app talks to you: a pop-up on top of everything when a blocked app is closed, a pop-up when the browser in front is showing a blocked site, the "blocked" page for sites, the notices when a window starts and ends, and a line of the day on the Today page. The lines are written for this app, not copied from his books or talks, and are kept clean of swearing. **A site pop-up means you actually went there.** Background tabs and programs look blocked names up all day (one open YouTube tab did it every minute and interrupted real work), so a lookup never causes a pop-up. Instead the tray looks at the window in front: Chrome and Edge title the "can't be reached" page with the address itself (`www.youtube.com - Microsoft Edge`), and only that, for a blocked address, pops up (at most once per site every 30 seconds). Firefox titles that page differently and gets no pop-up. A blocked program that keeps relaunching gets one pop-up per 30 seconds. Every attempt is still counted on the dashboard. Edit the lines in `internal/quotes/quotes.go`.

### Earned free time (rewards)

A parent can reward the child, for example for going to the gym, with free time that pauses a focus window.

1. **Parent:** *Parent area > Free time*. Pick a reason (quick buttons for Gym, Homework, Chores, or type your own), minutes (5 to 120) and how long it stays valid (tonight, 2 days, this week), then enter the passcode. **Every grant asks for the passcode again**, even in an unlocked dashboard.
2. **Child:** the *Today* page shows the saved-up time. *Use 15 min* or *Use all* starts a break straight away, **with no passcode**, because a parent already approved it. Focus time comes back by itself when the break ends, or earlier with *Back to focus now*; unused minutes are returned.
3. **Limits** (all enforced by the service, not the page): a daily maximum a parent can grant (default 60 minutes, adjustable); credit expires at midnight of its last valid day; the earliest-expiring credit is spent first; a break can never be longer than the focus time it pauses; only one break runs at a time; a break survives a restart of the service.
4. **Exam nights:** switch off *Earned free time can pause this window* in a window's settings and no credit can pause it. If a paused window overlaps one that cannot be paused, the second keeps blocking.
5. Time on a break does not count as focus time and does not break the focus streak. The activity log records every grant, break and refund.

Focus Guard cannot tell whether the gym visit happened. A grant is the parent's word; the app only makes sure it is spent within the limits the parent set.

The interface follows the flat "true-black" app style from the taste-skill design guide (no gradients, glows or borders on cards, one data colour, light and dark themes). It never waits on the network: every edit changes the screen first and saves in the background, and a save the service rejects is rolled back with a message. The first screen needs no request at all, because status and settings are embedded in the page.

The dashboard is a local web page served by the service. It binds to `127.0.0.1` only, checks the `Host` header (DNS-rebinding defence), requires a per-run token on every call (CSRF), checks `Origin`, and needs a passcode session cookie (HttpOnly, SameSite=Strict, 10 minute idle) for anything sensitive. Risky actions (ending a window, stopping the service) ask for the passcode again.

## Releasing an update

Regular commits never publish anything: pushing to any branch only runs `CI` (format, vet, tests, build). A release is published **only** when you push a version tag:

```powershell
scripts\release.ps1 -Version 1.2.0          # tests, tags v1.2.0, pushes, waits for the build, prints the release URL
scripts\release.ps1 -Version 1.2.0 -DryRun  # same checks + a build/sign run on GitHub that publishes nothing
```

(That is the same as `git tag v1.2.0` and `git push origin v1.2.0`; the script just checks first and waits for the result.) To pull a release onto **this** PC immediately instead of waiting, run `scripts\update-now.ps1` in an administrator PowerShell, or press **Install latest now** in *Parent area > Updates* (needs the passcode). Both refuse to restart the service during a focus window.

The `Release` workflow then runs the tests, builds `focusguard.exe` with the version baked in, builds `FocusGuard-Setup-1.2.0.exe` with Inno Setup, writes `SHA256SUMS`, signs it with the Ed25519 key in the `RELEASE_SIGNING_KEY` secret, and creates the GitHub Release with three files: the installer, `SHA256SUMS` and `SHA256SUMS.sig`. Run **Actions > Release > Run workflow** for a dry run that builds and signs everything but publishes nothing.

Installed copies:

- check the latest release shortly after startup (about 20 seconds) and then every hour, **only** from `Azaxek/focus-blocker` (compiled in, not configurable), over HTTPS, following redirects only to GitHub hosts;
- refuse anything that is not a strictly newer `vX.Y.Z` (no downgrades, no prereleases);
- verify the Ed25519 signature on `SHA256SUMS` against the public key built into the program, then the installer's SHA-256 against it, **before** running anything;
- install silently, but **only when no focus window is active and none starts within 5 minutes**, so there is no gap in protection. A ready update simply waits;
- run the install from a detached copy of the *old* version, which backs up the install folder, stops the service, installs, starts it and checks that the new version answers and stays up. If anything fails it restores the old files and restarts the old version automatically;
- never touch `C:\ProgramData\FocusGuard` (passcode hash, schedules, lists, logs). A test asserts that directory is byte-identical after both a successful update and a rollback.

*Parent > Updates* shows the installed version, last check, what is waiting, the auto-update switch and the update history (including rollbacks).

**Signing key.** The private key exists only as the repository secret `RELEASE_SIGNING_KEY`; the public key is `releasePubKey` in `cmd/focusguard/main.go`. To rotate: `go run ./tools/keygen --pair` prints the new private key (line 1) and public key (line 2); put the first into the secret, the second into `main.go`, and release a version signed with the **old** key that carries the new public key first.

**Optional but recommended:** in the repo's *Settings > Environments*, open the `release` environment and add a parent as a **required reviewer**. Then no release (and therefore no update) can ship without their approval. Without it, whoever can push a tag to this repo can ship any code to every install, including code that removes the passcode.

## Development

```powershell
go test ./...                              # everything below runs without administrator rights
go run ./cmd/focusguard run --dry --data .\devdata --port 47899   # dashboard on http://127.0.0.1:47899, touches nothing on the PC
                                           # (use --port when the real service is already running on 47820)
scripts\dev-install.ps1                    # (admin) build + install the real service from this tree
scripts\verify-enforcement.ps1 -Expect Blocking        # during a window, from a normal shell
scripts\dev-uninstall.ps1                  # (admin) remove everything the dev install added
```

Layout: `cmd/focusguard` (entry point), `internal/{clock,schedule,rewards,auth,store,apps,sites,engine,updater,server,winsvc,tray,app,fsx}`, `installer/` (Inno Setup), `tools/` (icon, keygen, signer), `.github/workflows/`.

`go test -race ./...` needs a C compiler on Windows (for example MinGW `gcc` on the PATH); CI runs it. The parsers have fuzz targets: `go test -run XXX -fuzz FuzzNormalize -fuzztime 30s ./internal/sites` (also `FuzzHostsMerge`, and `FuzzWindowValidate` in `./internal/schedule`).

## What is tested

Automated (`go test ./...`, also run by CI on Windows):

| Area | Covered |
| --- | --- |
| Scheduler | midnight crossover, `00:00` end, DST spring-forward and fall-back (America/Chicago), one-off, overlap and back-to-back, ended-early per occurrence |
| Clock | forward/backward jumps ignored and logged, one jump accepted after resume, correction against internet time, offline fallback |
| Passcode | only an argon2id hash on disk, salted, min length, 5 failures lock for 15 min, lock survives a restart, change requires the old one, never appears in the log |
| App blocking | name, folder, original-file-name, publisher and Store-package matching; protected system processes; a **real** process is closed in well under 2 s, a bystander survives |
| Site blocking | subdomain matching without look-alikes, hosts merge keeps other lines, tampering detected and restored, **real** sinkhole on ports 53/80 answers DNS and serves the block page |
| Updater | bad checksum, bad signature, foreign key, no key built in, wrong host / repo / http, downgrade, prerelease all rejected; rollback restores the old version; passcode and settings byte-identical after success and rollback |
| Dashboard API | Host allow-list, token, Origin / Sec-Fetch-Site, passcode session, cookie flags, lockout over HTTP, passcode re-entry for risky actions, no secrets in public endpoints, the status poll does not keep a session alive |
| Free time | grant limits and daily cap, expiry, earliest-expiring spent first, refund on early end, a break never outlasts its window, windows that opt out, overlapping windows, survives a restart, who may grant / spend / revoke; a randomised test checks that credit is never created or negative |
| Resilience | a damaged settings file is restored from its backup (or replaced with defaults) and the service still starts; a panic in one pass of the engine or watchdog is recovered; the dashboard port being taken does not stop the service; garbage and oversized packets do not stop the DNS sinkhole |
| Ending early and resuming | a window ended early can be turned back on while its time runs (no passcode), not before it began, after it is over, or if it was never ended; the early end stays on the record; it survives a restart; the endpoint needs the page token |
| Motivation | every line is non-empty, short and unique; the line of the day is stable within a day; a blocked-site lookup never produces a pop-up event but is still counted, a relaunching app gets one pop-up and carries a line; the tray recognises a blocked-site tab from its window title (and ignores ordinary page titles and look-alike domains); the block page names the site, the time and a line |
| Open connections | which connections are reset (a browser's, to a blocked site's address, on a web port, and nothing else); an elevated run also resets a real loopback connection (skipped without administrator rights) |
| Parsers | fuzzed: domain normalisation, hosts-file merge/clean, window validation; schedule invariants checked across seven zones and DST changes |

**Checked on a real installed copy:** blocking in Chrome and Edge, the app-closed notification, and the browser-extension proxy case. **Not yet verified on a real machine:** the installer itself, a real update from a published release, Safe Mode, and the tamper-restore timing (`scripts/tamper-test.ps1` runs it during a window; it needs one approval from an administrator). `scripts/verify-enforcement.ps1` checks enforcement automatically; the Release workflow's dry run builds the installer.

## Known limitations and bypass routes

Be honest with yourselves about these. None of the mitigations is perfect.

| Bypass | Why it works | Mitigation |
| --- | --- | --- |
| **Administrator rights** | An admin can stop the service, edit hosts/firewall/DNS, delete files, uninstall without the passcode. | Use a **standard** daily account; keep the admin password with the parent. Use [Windows Parental Controls / Microsoft Family Safety](https://account.microsoft.com/family) as a second layer. |
| **Browser proxy / VPN extensions** (for example a "VPN for Chrome" add-on) | A proxy resolves site names on its own side, so hosts, DNS and the sinkhole never see the request. **This was found in testing: with such an add-on on, a blocked site still loaded.** | During a window Focus Guard sets a browser policy that forces direct connections (Chrome, Edge, Brave, Firefox). Extensions cannot override a policy-set proxy mode, and the policy is removed when the window ends. Tested with a stand-in proxy and headless Chrome; the real add-on has not been tried. |
| **System-level VPN apps, Tor, SOCKS/HTTP proxies used by other programs** | Programs that tunnel their own traffic can skip the browser policy. A system VPN still resolves names through Windows, so the hosts and DNS rules apply, but that is **not tested**. | Standard users cannot install system VPNs, but can use portable browsers or web proxies. Block proxy/VPN domains in the site list; use router-level filtering too; consider AppLocker/WDAC to allow only approved programs. |
| **A phone, console or second device** | Focus Guard only controls this PC. | Router or DNS filtering (e.g. NextDNS, OpenDNS Family Shield) and phone screen-time tools. |
| **Booting from USB / another OS** | Offline, nothing runs. | BIOS/UEFI password, disable USB boot, BitLocker (also protects files from offline editing). |
| **Reinstalling Windows** | Wipes everything. | Same as above: firmware password and a parent-owned recovery key. The activity log shows nothing, so a missing install is itself the signal. |
| **Safe mode** | Services normally do not start in Safe Mode, and anyone can reach it from the sign-in screen (Shift + Restart). | The installer registers the service for Safe Mode (minimal and with networking), so it should keep running there. This is set up but **not yet tested on a real machine**. The hosts, DNS and firewall rules also persist on disk. A firmware password and BitLocker (which asks for the recovery key when boot settings change) back this up. |
| **Pushing a modified version through auto-update** | The updater installs whatever is signed by the release key. Someone who controls the repo's tags and its secret/workflow can ship code that disables the lock. | Updates are signed (Ed25519) and verified against a key compiled into the app, so an outsider or a tampered download is rejected. For the repo owner: set a **required reviewer** on the `release` environment, keep the signing secret out of reach, and have a parent own or review the repo. Turn **automatic updates off** (Parent > Updates) if the repo cannot be trusted. |
| **Changing the PC clock while offline, then rebooting** | Without internet there is nothing to compare the clock against. | While the service runs, jumps are ignored. After a reboot the first internet check corrects the clock. Keep the machine online; an unplugged-network reboot is a known gap. |
| **Existing connections** | Blocking happens at name lookup, and a browser that already holds an open connection to a site reuses it for new requests without looking the name up again. **Found in real use:** a manga site stayed loadable in Edge for hours because Edge kept one connection to it open, while a fresh Edge profile was refused. | When a block starts, and for two minutes after, the service finds the blocked sites' real addresses (a direct DNS query to public resolvers) and resets every open IPv4 web connection a browser holds to them, so the next request has to look the name up and gets the sinkhole. Gaps: IPv6 connections cannot be reset this way, other subdomains that live on different addresses are not looked up, and programs that are not browsers are left alone. Closing and reopening the browser still closes any gap. |
| **Portable browsers with their own DoH** | A copy of Firefox or Chromium run from a USB stick ignores the registry policies. | The firewall rules block known public DoH/DoT resolvers, and a canary domain turns Firefox's DoH off by default; custom DoH servers on unknown IPs are not covered. Use AppLocker/WDAC to forbid unapproved executables. |
| **Modified or re-signed copies of a blocked app** | App matching trusts the file name, folder, version-info and signature fields, and an attacker can edit all of them on a copy outside any blocked folder. | Allow-listing (AppLocker/WDAC) is the only strong answer. Use the "list running apps" helper to add the exact executable name. |
| **Wi-Fi/ethernet changes that bypass NRPT** | Rare resolvers or per-app DNS clients may ignore NRPT. | The hosts-file layer covers the exact domain and `www`; the watchdog re-checks every 3 seconds. |
| **Antivirus flags the hosts file edit** | Some security products treat any hosts change as suspicious. | Add an exclusion for `C:\Program Files\FocusGuard`. |
| **Windows time zone changed** | A standard Windows user is allowed to change the time zone, which would move every window by hours after the next restart. | The schedule is **pinned** to a time zone chosen in the dashboard (set automatically the first time a parent unlocks it; change it in *Parent area > Security*). The dashboard shows a notice when the PC's zone no longer matches. |
| **Port 53 or 80 already in use on 127.0.0.1** | Some VPNs and Hyper-V/WSL tools bind them. | The hosts file, firewall and policies still apply, but subdomain wildcards and the block page do not. The dashboard shows an "Enforcement problem" banner. |
| **Another program holding port 47820** | The dashboard cannot start, and a fake page there could ask for the passcode. | Blocking carries on regardless and the service keeps retrying; the log records it. Only type the passcode into the dashboard opened from the Focus Guard shortcut or tray icon. |
| **A free-time grant the child did not earn** | The app cannot verify the gym visit or the homework. | Grants need the passcode every time, are capped per day, expire, and are all logged. |

### If the parent passcode is lost

This is an administrator-only recovery. As an administrator: `sc stop FocusGuard`, delete `C:\ProgramData\FocusGuard\auth.json`, `sc start FocusGuard`, then open the dashboard and create a new passcode. Schedules and lists are kept. (This is exactly why a standard account matters.)

### Uninstalling

*Settings > Apps > Focus Guard > Uninstall*, as an administrator. The uninstaller asks the running service for approval, so the **parent passcode** must be entered in the Focus Guard window, then it removes the service, hosts block, DNS and firewall rules, browser policies, files and data. If the service is already stopped, run `scripts\dev-uninstall.ps1` as an administrator.

## Licence

None yet, so by default all rights are reserved. Add a `LICENSE` file if you want others to be able to reuse the code.
