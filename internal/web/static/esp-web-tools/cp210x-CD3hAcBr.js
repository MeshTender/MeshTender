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
import{c as e,f as t,a as i}from"./index-rScOpSrP.js";const r=e=>{const t={none:0,odd:16,even:32}[e.parity],i=2===e.stopBits?2:0;return e.dataBits<<8|t|i},n=(e,t)=>768|(e?1:0)|(t?2:0);class s{constructor(e){this._device=e,this._interfaceNumber=0}_out(t,i,r){return e(this._device,{requestType:"vendor",recipient:"interface",request:t,value:i,index:this._interfaceNumber},r)}async open(){const e=t(this._device,255);if(!e)throw new Error("No vendor specific interface found");return this._interfaceNumber=e.interfaceNumber,await this._device.claimInterface(e.interfaceNumber),await this._out(0,1),i(e)}async setLineConfig(e){const t=new Uint8Array(4);new DataView(t.buffer).setUint32(0,e.baudRate,!0),await this._out(30,0,t),await this._out(3,r(e))}setSignals(e,t){return this._out(7,n(e,t))}setBreak(e){return this._out(5,e?1:0)}async close(){await this._out(0,0)}}const a=e=>new s(e);export{r as cp210xLineControl,n as cp210xModemControl,a as createCp210xDriver};
