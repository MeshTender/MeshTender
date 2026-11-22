/*! esp-web-tools 10.5.2 | Apache-2.0 | https://github.com/esphome/esp-web-tools
 * Bundles Lit 3.3.3 (BSD-3-Clause, Copyright (c) 2017 Google LLC); Material Web
 * 2.5.0 (Apache-2.0, Copyright 2018-2023 Google, Inc.); Improv Wi-Fi serial SDK
 * 3.0.0 (Apache-2.0); esptool-js 0.7.0 (Apache-2.0, Copyright 2019 Espressif
 * Systems (Shanghai) CO LTD) with esp-flasher-stub 1.2.2 (MIT OR Apache-2.0,
 * Copyright (c) 2026 Espressif Systems (Shanghai) CO LTD); pako 2.2.0 (MIT AND
 * Zlib, Copyright (C) 2014-2017 Vitaly Puzrin and Andrei Tuputcyn, (C) 1995-2013
 * Jean-loup Gailly and Mark Adler); atob-lite 2.0.0 (MIT, Hugh Kennedy); tslib
 * 2.9.1 (0BSD, Copyright (c) Microsoft Corporation). Full license texts are in
 * THIRD-PARTY-NOTICES.md. Banner added by MeshTender; the build ships none. */
import{f as e,c as t,a as r}from"./index-rScOpSrP.js";const i={none:0,odd:1,even:2},n=e=>{const t=new Uint8Array(7);return new DataView(t.buffer).setUint32(0,e.baudRate,!0),t[4]=2===e.stopBits?2:0,t[5]=i[e.parity],t[6]=e.dataBits,t};class s{constructor(e){this._device=e,this._controlNumber=0}async open(){var t;const i=e(this._device,2);if(!i)throw new Error("No CDC control interface found");const n=null!==(t=e(this._device,10))&&void 0!==t?t:i;return this._controlNumber=i.interfaceNumber,await this._device.claimInterface(i.interfaceNumber),n!==i&&await this._device.claimInterface(n.interfaceNumber),r(n)}setLineConfig(e){return t(this._device,{requestType:"class",recipient:"interface",request:32,value:0,index:this._controlNumber},n(e))}setSignals(e,r){return t(this._device,{requestType:"class",recipient:"interface",request:34,value:(e?1:0)|(r?2:0),index:this._controlNumber})}setBreak(e){return t(this._device,{requestType:"class",recipient:"interface",request:35,value:e?65535:0,index:this._controlNumber})}}const c=e=>new s(e);export{n as cdcLineCoding,c as createCdcAcmDriver};
