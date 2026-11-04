# go2rtc with H.265 fixes

A Home Assistant add-on that builds [go2rtc](https://github.com/AlexxIT/go2rtc) from source with a patch for receiving H.265
over WebRTC. It is a personal experiment, not an official release. It can run next to the official go2rtc add-on.

What the patch changes in go2rtc:

- **Packets back in order.** WebRTC runs over UDP, so packets can overtake each other, and a retransmitted packet always
  arrives after the ones sent later. go2rtc treated any of these as lost. Now they are held for up to 400 ms and put back.
- **Broken frames are dropped.** When a packet really is lost, the frame and everything after it are dropped until the next
  keyframe, so a decoder gets a short freeze instead of corrupted data.
- **Regular keyframes.** A keyframe is requested every 2 seconds, also from cameras go2rtc connects to itself.
- **Aggregation packets.** RFC 7798 packets with several NAL units are split up, the same fix as upstream
  [PR 2296](https://github.com/AlexxIT/go2rtc/pull/2296).

Every 10 seconds the log shows counters: `[h265] frames=… dropped=… keys=… keysDamaged=… held=… filled=… late=… lost=… gaps=… maxGap=…`.

The build runs the tests in `go2rtc_hevc/patch/depay_test.go`, and stops if they fail.

## Install

Add this repository under *Settings → Add-ons → Add-on store → ⋮ → Repositories*, and install **go2rtc H.265**. Home Assistant
builds it on your machine, which takes a few minutes.

## Options

| Option | Meaning |
|---|---|
| `api_port` | Port of go2rtc's web interface and API |
| `api_username`, `api_password` | Login for the API; the add-on does not start without a password |
| `streams` | List of `name` and `url`, in go2rtc's stream syntax |
