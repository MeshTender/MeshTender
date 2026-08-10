// One way to open a serial port, whatever the browser offers.
//
// Both serial callers (serial-setup.js talking the repeater CLI, console.js
// bridging a KISS modem) need exactly four things from a port: open it at a baud
// rate, read bytes, write bytes, close it. This module supplies that as
// window.MeshSerial, backed by whichever transport is actually available:
//
//   Web Serial (navigator.serial) — Chrome/Edge on desktop, Firefox 151+ on
//     desktop. Preferred everywhere EXCEPT Android; MeshSerial just forwards to it.
//
//   WebUSB (navigator.usb) — the fallback that makes Android work. Chrome for
//     Android has had WebUSB since Chrome 61, while Web Serial only reached Android
//     in Chrome M149 and its USB half depends on a platform API that shipped to a
//     limited set of devices. So on a phone, WebUSB is the transport that exists.
//
// Hence chooseTransport() preferring WebUSB on Android specifically. Feature
// detection cannot make that call for us: on a current Chrome for Android
// navigator.serial is present and looks healthy, and the emptiness only shows up
// as a chooser with no devices in it. There is no enumeration API to pre-check,
// and requestPort() rejects with NotFoundError for both "user cancelled" and "no
// devices" — so we cannot even detect it after the fact and retry. Platform is
// the only signal available, which is why this file sniffs one.
//
// The WebUSB path speaks USB CDC-ACM — the standard "USB serial" device class —
// by claiming the interfaces itself and driving the two class requests that
// configure the line. Constants and the open sequence are from the USB CDC
// specification v1.1 (§6.2.12 Set_Line_Coding, §6.2.14 Set_Control_Line_State).
//
// IMPORTANT — what this does NOT cover: boards whose USB is a vendor-specific
// bridge chip (CP210x, CH340, FTDI) are not CDC-ACM. They enumerate with a
// vendor interface class and speak a proprietary control protocol per chip, so
// requestPort() will not offer them. MCUs with native USB (nRF52840, ESP32-S3/C3)
// are CDC-ACM and work. A CP210x driver is a separate, additive change: it plugs
// in as another transport behind this same facade.

(function () {
  "use strict";

  // USB CDC interface classes. The control interface carries the class requests;
  // the data interface carries the bulk endpoints that move bytes.
  const CDC_CONTROL_CLASS = 0x02;
  const CDC_DATA_CLASS = 0x0a;

  // USB CDC class requests (spec v1.1 §6.2).
  const REQ_SET_LINE_CODING = 0x20;
  const REQ_SET_CONTROL_LINE_STATE = 0x22;

  const hasWebSerial = typeof navigator !== "undefined" && "serial" in navigator;
  const hasWebUSB = typeof navigator !== "undefined" && "usb" in navigator;

  // isAndroid reads the UA client hint where it exists and falls back to the UA
  // string. This picks between two transports that both work — it never gates
  // support — so a wrong answer costs a less-capable transport, not a broken page.
  function isAndroid() {
    const hints = navigator.userAgentData;
    if (hints && typeof hints.platform === "string") return hints.platform === "Android";
    return /Android/i.test(navigator.userAgent || "");
  }

  // chooseTransport resolves the preference order once, at load.
  function chooseTransport() {
    if (hasWebUSB && isAndroid()) return "webusb";
    if (hasWebSerial) return "webserial";
    if (hasWebUSB) return "webusb";
    return null;
  }

  const transport = chooseTransport();

  // findInterface returns the first interface whose (first alternate) class is
  // `cls`, or null. Called after the configuration is selected, so
  // device.configuration is populated.
  function findInterface(device, cls) {
    const config = device.configuration;
    if (!config) return null;
    for (const iface of config.interfaces) {
      const alt = iface.alternates[0];
      if (alt && alt.interfaceClass === cls) return iface;
    }
    return null;
  }

  // findEndpoint returns the first bulk endpoint on `iface` in `direction`
  // ("in" or "out"), or null.
  function findEndpoint(iface, direction) {
    const alt = iface.alternates[0];
    if (!alt) return null;
    for (const ep of alt.endpoints) {
      if (ep.direction === direction && ep.type === "bulk") return ep;
    }
    return null;
  }

  // CdcAcmPort presents the subset of the Web Serial SerialPort interface our
  // callers use — open(), readable, writable, close() — on top of a USBDevice.
  class CdcAcmPort {
    constructor(device) {
      this.device_ = device;
      this.control_ = null;
      this.data_ = null;
      this.in_ = null;
      this.out_ = null;
      this.readable = null;
      this.writable = null;
      // Set before we tear the device down, so a transfer that was already in
      // flight can fail quietly instead of surfacing as a read error.
      this.closing_ = false;
    }

    async open(options) {
      const baudRate = (options && options.baudRate) || 115200;
      await this.device_.open();
      if (this.device_.configuration === null) {
        // configurations[0] rather than a hardcoded 1: the first configuration
        // is the one we searched for interfaces, whatever its value.
        await this.device_.selectConfiguration(this.device_.configurations[0].configurationValue);
      }

      this.control_ = findInterface(this.device_, CDC_CONTROL_CLASS);
      this.data_ = findInterface(this.device_, CDC_DATA_CLASS);
      if (!this.control_ || !this.data_) {
        await this.abandon_();
        throw new Error(
          "This USB device isn't a standard serial (CDC-ACM) device. Boards with a " +
          "CP210x, CH340, or FTDI bridge chip aren't supported over WebUSB yet."
        );
      }
      this.in_ = findEndpoint(this.data_, "in");
      this.out_ = findEndpoint(this.data_, "out");
      if (!this.in_ || !this.out_) {
        await this.abandon_();
        throw new Error("This USB device has no usable serial data endpoints.");
      }

      try {
        await this.device_.claimInterface(this.control_.interfaceNumber);
        if (this.data_.interfaceNumber !== this.control_.interfaceNumber) {
          await this.device_.claimInterface(this.data_.interfaceNumber);
        }
        await this.setLineCoding_(baudRate);
        await this.setControlLineState_(true);
      } catch (e) {
        await this.abandon_();
        // A claim failure almost always means the OS driver owns the device,
        // which is the common desktop-Linux case and worth naming.
        throw new Error("Could not take control of the USB device: " + e.message);
      }

      this.readable = new ReadableStream(this.source_(), { highWaterMark: 0 });
      this.writable = new WritableStream(this.sink_());
    }

    // setLineCoding_ configures the line as 8-N-1 at `baudRate`. Devices with
    // native USB ignore the values (there is no real UART behind them), but the
    // request is part of bringing an ACM device up, so we always send it.
    // Payload layout is spec v1.1 §6.2.12: dwDTERate, bCharFormat, bParityType,
    // bDataBits.
    async setLineCoding_(baudRate) {
      const buf = new ArrayBuffer(7);
      const view = new DataView(buf);
      view.setUint32(0, baudRate, true); // little-endian
      view.setUint8(4, 0); // 1 stop bit
      view.setUint8(5, 0); // no parity
      view.setUint8(6, 8); // 8 data bits
      const result = await this.device_.controlTransferOut({
        requestType: "class",
        recipient: "interface",
        request: REQ_SET_LINE_CODING,
        value: 0,
        index: this.control_.interfaceNumber,
      }, buf);
      if (result.status !== "ok") throw new Error("failed to set line coding (" + result.status + ")");
    }

    // setControlLineState_ raises or drops DTR. Asserting DTR is what tells a
    // CDC device the host is present; without it many firmwares never start
    // sending. Bitmap is spec v1.1 §6.2.14: bit 0 DTR, bit 1 RTS.
    async setControlLineState_(on) {
      await this.device_.controlTransferOut({
        requestType: "class",
        recipient: "interface",
        request: REQ_SET_CONTROL_LINE_STATE,
        value: on ? 0x01 : 0x00,
        index: this.control_.interfaceNumber,
      });
    }

    // source_ reads one USB packet per pull. Reading a single packet (rather
    // than a multi-packet buffer) keeps latency predictable for the KISS bridge
    // and avoids a transfer that sits unfinished waiting to be filled.
    source_() {
      const port = this;
      return {
        async pull(controller) {
          try {
            const result = await port.device_.transferIn(port.in_.endpointNumber, port.in_.packetSize);
            if (port.closing_) return;
            if (result.status !== "ok") {
              controller.error(new Error("USB read failed (" + result.status + ")"));
              return;
            }
            if (result.data && result.data.byteLength) {
              controller.enqueue(new Uint8Array(
                result.data.buffer, result.data.byteOffset, result.data.byteLength));
            }
          } catch (e) {
            if (port.closing_) return; // we pulled the device out from under it
            controller.error(e);
          }
        },
      };
    }

    sink_() {
      const port = this;
      return {
        async write(chunk, controller) {
          try {
            const result = await port.device_.transferOut(port.out_.endpointNumber, chunk);
            if (result.status !== "ok") {
              controller.error(new Error("USB write failed (" + result.status + ")"));
            }
          } catch (e) {
            controller.error(e);
          }
        },
      };
    }

    // abandon_ closes the device after a failed open, without the stream
    // teardown close() does — there are no streams yet at that point.
    async abandon_() {
      try { if (this.device_.opened) await this.device_.close(); } catch (_) {}
    }

    async close() {
      this.closing_ = true;
      // Callers release their reader/writer locks before close(), but cancel()
      // and abort() throw on a still-locked stream, so neither is load-bearing.
      try { if (this.readable) await this.readable.cancel(); } catch (_) {}
      try { if (this.writable) await this.writable.abort(); } catch (_) {}
      this.readable = null;
      this.writable = null;
      if (this.device_.opened) {
        try { await this.setControlLineState_(false); } catch (_) {}
        try { await this.device_.close(); } catch (_) {}
      }
    }
  }

  // requestPort prompts the user to pick a device and returns a port. Must be
  // called from a user gesture, same as navigator.serial.requestPort().
  async function requestPort() {
    if (transport === "webserial") return navigator.serial.requestPort();
    if (transport === "webusb") {
      // Filtering on the CDC control class keeps the chooser to devices we can
      // actually drive, rather than listing every USB device attached. Per the
      // WebUSB matching algorithm a classCode filter is compared against each
      // interface as well as the device descriptor, so this still matches the
      // usual native-USB board, which declares class 0xEF (IAD) at device level
      // and CDC only on its interfaces.
      const device = await navigator.usb.requestDevice({
        filters: [{ classCode: CDC_CONTROL_CLASS }],
      });
      return new CdcAcmPort(device);
    }
    throw new Error("This browser can't connect to USB devices.");
  }

  window.MeshSerial = {
    // supported answers "can we even ask for a port here?" — not "will this
    // particular board work", which is only knowable once one is chosen.
    supported: transport !== null,
    // transport lets callers tailor their messaging: the ways this fails differ
    // between the two (no port vs. a device we can't drive).
    transport: transport,
    requestPort: requestPort,
  };
})();
