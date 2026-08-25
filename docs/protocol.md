# Xiaomi Portable Photo Printer Pro — USB Protocol

Printing has been confirmed to work end-to-end on the actual device (2026-08-25). This file has 3 parts:

1. **Static analysis part** (this section) — findings read out of
   `libht_device_usb_core.dylib` (C++, symbols not stripped) bundled with Liene Photo for macOS `2.4.212.02`.
   This is an implementation for **different models** such as the PixCut S1, and **both the frame format and the method names turned out to differ from this device**
2. **Facts verified on the device** — up to the discovery of the debug shell
3. **Deciphering the JSON-RPC and the successful print** — the procedure that actually works on this device. **This is what the implementation should reference**

What follows first is the content of part 1. Note that some of it does not apply to this device.

## 1. Device identification

```
USB VID = 0x302C (Hannto)   PID = 0x3012
IEEE1284 Device ID = MFG:Hannto;MDL:MDK_NS_PO_NOMSC;CMD:;
```

(This `CMD:;` is the value CUPS shows. In reality **the response differs per interface**, and IF#0
returns `CMD:DC_RASTER`. See the on-device verification part below)

Empty `CMD:` = no PostScript/PCL support. Standard printer drivers cannot print to it.

There are 2 USB interfaces:

| IORegistry name | Purpose |
|---|---|
| `Print@0` | Command channel (`DeviceConnector::writeCommand` / `readCommand`) |
| `HanntoUSB@2` | Data channel (`DeviceConnector::writeData` / `readData`) |

On Liene models the descriptor strings are `WinEg` (command) / `WinE2` (data). The mapping on this device needs to be confirmed.

## 2. Framing (`ATTProtocolManager`)

The header is an ASCII string. There is no length prefix and no checksum.

### Command frame

```
"cmd json\n" + <JSON string>
```

`wrapJsonCommand()` merely concatenates the header and the JSON. Responses use the same format, and
`isATTJsonResponse()` uses `memcmp` to check whether the payload starts with `"cmd json\n"`.

### Data frame

```
"cmd data EXTLEN=" + <decimal length> + "\n" + <job-id: uint32 LE> + <payload>
```

- `encodeJobIdLE` / `decodeJobIdLE` read/write 4 bytes in little-endian
- `getMaxPacketSize()` = **0x2800 (10240) bytes**
- `getEffectivePayloadSize()` = 10240 − header length. The header length is variable, depending on the number of digits in the decimal length

## 3. JSON-RPC (`RawProtocolManager`)

```json
{"method":"<name>","params":<...>,"id":<sequence number>}
```

Responses carry either `result` or `error`.

### print-job

```json
{"method":"print-job","params":{
  "job-type":  <int>,
  "media-size":<int>,
  "media-type":<int>,
  "copies":    <int>,
  "channel":   <int>,
  "file-size": <int>
},"id":<n>}
```

Extract `job-id` from the response (the `job_id` spelling is also accepted) and send the JPEG to the data channel tagged with that ID.
**What the host sends is the JPEG itself.** Color separation, dithering, and ribbon control are all done on the firmware side.

### Other methods

`get-prop` (params is an array of strings) / `set-prop` / `device-info` / `combo-job` / `cut-job` /
`get-job-info` / `cancel-job` / `resume-printer` / `ribbon-info` / `ota-info` /
`upgrade-firmware` / `auto-off-interval`

props: `printer-state` `printer-sub-state` `printer-state-alerts` `battery` `media-type`
device-info: `firmware-version` `model` `sn` `maintenance`

## 4. About encryption

The USB path has **neither DH key exchange nor AES** (no matching symbols or strings exist). Plaintext ASCII header + JSON.
The DH/AES mentioned in the README of [pixcut](https://github.com/honeymaro/pixcut) appears to concern the Bluetooth path or
a different protocolType. Needs confirmation.

## 5. Values still undetermined at this point (→ resolved in the latter half)

- The numeric values of `job-type` / `channel` (the value denoting USB)
- This device's `media-size` / `media-type` codes (for the sibling ZINK model `kiwi`, 2x3 = 5011, Zink paper = 3000.
  This device is dye-sublimation, so different values are likely. Reading `media-type` via `get-prop` should tell)
- The endpoint addresses of the command / data interfaces

---

# Facts verified on the device (2026-08-24)

The parts that static analysis alone could not answer were verified on the actual device (powered on, paper and cartridge loaded).

## IF#0 declares DC_RASTER

Issuing the USB printer-class `GET_DEVICE_ID` (bmRequestType=0xA1, bRequest=0) per interface returns
**a different response for each interface**.

| IF | Response |
|---|---|
| #0 `Print` | `MFG:Hannto;MDL:MDK_NS_PO_NOMSC;CMD:DC_RASTER;CLS:PRINTER;` |
| #1 `Battery=4,GRN` | `MFG:Hannto;MDL:MDK_NS_PO_NOMSC;CMD:;` |
| #2 `HanntoUSB` | `MFG:Hannto;MDL:MDK_NS_PO_NOMSC;CMD:;` |

The `CMD:;` that CUPS shows is the response from interfaces other than IF#0. **IF#0 accepts a Hannto-proprietary raster format called `DC_RASTER`**.
`GET_PORT_STATUS` (bRequest=1) STALLs on all IFs (not implemented).

## IF#2 (EP 0x06/0x86) is a text debug shell

Writing newline-terminated text commands to bulk OUT 0x06 yields responses on bulk IN 0x86.

```
> help
support cmd: adpt echo cdb cmd log sensor i2c ui producttest bd bat apd hid bt otp
```

Verified commands:

| Command | Description |
|---|---|
| `echo <s>` | Echo |
| `apd` | Bulk dump of sensors and power (paper sensors, cover open/close, head temperature, battery voltage) |
| `bat data\|soc\|voltage\|status\|temp\|charge\|slidingAdc` | Battery information |
| `hid bp=<n>` / `hid caf p\|r` / `hid cs p\|r` | HID event injection (battery-level notification, camera AF / shutter) |
| `bt AT+<cmd>` | AT passthrough to the BT module. `AT+MIPID/MIMAC/MINAME/MIDID/MIBKEY/MIVER/MIBIND/MIRESET` |
| `bt pdu_usb=<len>,<data>` / `bt cmd_usb=<len>,<cmd>` | Data passthrough to the BT module |
| `bt power on\|off` / `bt remain_conn` | BT power / connection count |
| `cmd json` | **Entry point of the JSON-RPC** |
| `adpt` `cdb` `bd` `ui` `log` `sensor` `i2c` `otp` `producttest` | Not investigated (`producttest` and `i2c` are left untouched because their side effects are unpredictable) |

Measured example of `apd`:

```
PSEN_A adc=3.277V=8136 / PSEN_B adc=3.297V=8184 / RSEN adc=0.213V=529
TPH adc=1.465V (30.988)=3637      ← thermal head temperature
BAT voltage=6798, 8.215V, full(4) / BAT 1 temp=4033, 29.5 degree
senA=0 senB=0 cover=1             ← paper sensors / cover
```

Measured examples of `bat` subcommands (2026-08-26):

```
> bat soc
soc: 95
> bat charge
chargeStatus: 1
> bat data
raw_adc: 6859.0,sliding_window_adc: 6857.8,filtered_adc: 6865.7,voltage: 4.154,
soc: 95,status: 4,temp_raw_adc: 4070.0,temp_charge: 28.94,temp_not_charge: 25.00
```

## JSON-RPC frame format (not yet determined at this point → resolved in the next chapter)

Result of brute-forcing the format that follows `cmd json`:

| Sent | Response |
|---|---|
| `"cmd json\n" + JSON` | **No response** |
| `"cmd json\n" + uint32LE(len) + JSON` | `{"method":"event.rpt_err","params":{"code":-8201,"message":"json parse error"}}` |
| `"cmd json <JSON>\n"` (space-separated) | `... ER` (the shell rejects it as an argument) |
| `"cmd json EXTLEN=<len>\n" + JSON` | No response |

**The JSON-RPC engine itself is being reached** (a parse error comes back). The no-response cases are consistent with
the parser trying to read a length field, consuming the first 4 bytes of the JSON, and then waiting forever for the rest.
→ The form `"cmd json\n" + <length> + <one more field> + JSON` looks promising. Next time, brute-force this.

Note that the `libht_device_usb_core.dylib` for the PixCut S1 (`wrapJsonCommand`) just sends `"cmd json\n" + JSON`, so
**the frame format differs from this device** (in the registry, too, the PixCut family is `protocolType 1`, while this device appears to be a different lineage).

## Operational notes

- The printer **auto powers off** after a few minutes and disappears from the USB tree. It needs to be powered back on during work
- There is a method to set `auto-off-interval`, so once JSON-RPC works, extending it should be possible

---

# Deciphering the JSON-RPC (2026-08-25, verified on the device)

## Frame format (confirmed)

```
"cmd json\n" + <JSON string>
```

A length field is **not needed**. `EXTLEN=` is not needed either (it works if present, but is redundant).
Inserting a binary length makes the JSON parser consume it along with the JSON and return
`{"method":"event.rpt_err","params":{"code":-8201,"message":"json parse error"}}`.

## Method names are underscore-separated

Where the official dylib (PixCut S1 family) uses `get-prop` / `print-job`, **this device uses `get_prop` / `print_job`**.
The hyphenated versions get no response (unknown methods are silently dropped without even an error).

| method | params | Response |
|---|---|---|
| `get_prop` | array | `{"result":[{}, ...]}` — one element per request, but **the vocabulary is unknown and it always returns empty objects** |
| `print_job` | see below | `{"result":{"job_id":<n>}}` |
| `job_info` | `[<job_id>]` or `{"job_id":<n>}` | Job status (see below) |
| `cancel_job` | both `[<id>]` / `{"job_id":<id>}` yield `-8001 Job ID Ivalid` | **The correct format is not yet determined** |
| `resume_printer` | `[]` | `{"result":["OK"]}` |

Unknown methods (confirmed by no response): `set_prop` `create_job` `combo_job` `cut_job` `dev_info` `info` `status`
`version` `ping` `echo` `upgrade` `ota` `print` `start_print` `get_battery` `get_sn` `get_model` and many more.

## print_job

```json
{"method":"print_job","params":{
  "job_type":0, "media_size":5011, "media_type":2010,
  "copies":1, "channel":2, "file_size":<JPEG byte count>
},"id":<n>}
```

→ `{"result":{"job_id":90},"id":<n>}`

- With these parameters, `job_state` becomes **`waiting`** (awaiting data). With empty parameters it immediately becomes `aborted`
- `media_size` 5011 = 2x3, `media_type` 2010 = photo paper (the same values as `极印打印机N3` in the Chinese registry)
- **If no data is sent, the job automatically becomes `aborted` after a while and no paper is consumed** (measured and confirmed)

## job_info response

```json
{"result":[{"job_id":90,"job_type":0,"job_state":"waiting","prt_copies":0,
 "transfer_time":0,"print_time":0,"clients":0,
 "did":"5000367924","fw_ver":"2.1.2_0039"}],"id":41}
```

`job_state`: `waiting` → (data transfer) → printing → done / `aborted`.
This unit's firmware is **2.1.2_0039**, and its Xiaomi device id is 5000367924.

## Issues still open at this point (→ resolved in the next chapter, "Successful print")

1. **The data transfer path and format**. The official dylib sends
   `"cmd data EXTLEN=<len>\n" + job_id(uint32 LE) + <JPEG>` to EP 0x04/0x84, but this device has no 0x04/0x84.
   Either `cmd data` goes to the same shell (EP 0x06), or it goes to IF#0 (EP 0x01), which declares DC_RASTER
2. The `get_prop` prop vocabulary (hyphenated / underscored `printer_state` variants and siid/piid forms all return empty)
3. The correct params for `cancel_job`

---

# Successful print (2026-08-25) — the complete procedure

Successfully printed one sheet over USB. **Everything is done over EP 0x06 (OUT) / 0x86 (IN), i.e. the single channel of IF#2.**
Neither IF#0, which declares `DC_RASTER`, nor EP 0x04/0x84 used by the official dylib is used.

## Procedure

### 1. Job creation

```
cmd json\n{"method":"print_job","params":{"job_type":0,"media_size":5011,
  "media_type":2010,"copies":1,"channel":2,"file_size":<JPEG byte count>},"id":<n>}
→ {"result":{"job_id":92},"id":<n>}
```

Put the exact byte count of the JPEG to be sent into `file_size`. At this point `job_state` is `waiting`.

### 2. Data transfer (chunked)

Write each chunk to EP 0x06 in the following format:

```
"cmd data EXTLEN=" + <len> + "\n" + <job_id: uint32 LE> + <JPEG chunk>
```

- **`len` = chunk length + 4** (a length that includes the 4 bytes of job_id)
- The total per packet must be 10240 bytes or less (matching `getMaxPacketSize()` = 0x2800).
  Values used in practice: chunk 10208 bytes + header 22 + job_id 4 = 10234 bytes
- Each chunk gets a `cmd data EXTLEN=<len> OK\n` response. Wait for it before sending the next one

### 3. Progress monitoring

```
cmd json\n{"method":"job_info","params":[<job_id>],"id":<n>}
```

`job_state` transitions:

```
waiting → downloading → printing → finished
```

`transfer_time` / `print_time` (milliseconds) are also returned. Measured: for a 123,583-byte JPEG,
transfer 10.9 seconds, printing 69.1 seconds.

### Out of paper is `pending` (measured 2026-08-25)

When printing with no paper, print_job and the chunk transfer are accepted without errors, and the job transitions
`downloading → printing` (feed attempt for ~20 seconds) → **`pending`**, where it then waits.
**No error notifications such as `rpt_err` are sent at all** (`job_state` is the only clue).

- **Loading paper during `pending` makes the job resume automatically** and it runs through `printing → finished` (measured)
- The `apd` sensor values **do not change with the presence or absence of paper** (`senA/senB/PSEN` all identical; they appear to be sensors used during the feed motion).
  A static paper-presence check is not possible
- An open cover can be detected via `apd`'s **`cover=0`** (closed = 1). When the cover is open, `RSEN` also
  changes from 3.297V → 0.979V (apparently due to the ribbon being exposed)

## Image requirements

- **Send the JPEG as-is**. Color separation, dithering, and the 4-pass ribbon control are on the firmware side
- Sending at a resolution of 626×939 px (2×3 inch @ 313dpi) printed correctly
- `media_size` 5011 = 2x3, `media_type` 2010 = photo paper

## What was confirmed about safety

- Jobs created by `print_job` with no parameters, and jobs for which no data was sent, automatically become
  `aborted` after a while, and **no paper is consumed**
- `cancel_job` returns `-8001 Job ID Ivalid` for both `[id]` / `{"job_id":id}`.
  The correct format is not yet determined, but since jobs auto-abort if left alone, the practical impact is small

## Effective print area (measured)

Result of printing a 626×939 test chart:

| Chart frame | Position | Appeared on paper? |
|---|---|---|
| Black frame | 0–2px from the edge | **Does not appear** |
| Red frame | 8px from the edge | Barely appears |
| Blue frame | 20px from the edge | Appears |

→ **Roughly 6–8px around the perimeter is cropped/trimmed off** (overscan for borderless printing).

Implementation policy: resize the input image to 626×939 with cover and send it (printing borderless is the correct behavior).
Use 16px inset from the edge as the guideline for the safe area in the UI.
