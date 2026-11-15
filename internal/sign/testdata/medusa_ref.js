// x-medusa 参考实现（确定性版本），用于生成 Go 移植的黄金向量。
//
// 逐行抄录自 hongguo-open-player/src/server/native/app-source.js（GPL-3.0-only）
// 的 hashF13 链、AesV3、xmxor、keyHash、deviceProto 与 buildMedusa；
// 唯一改动：把 Math.random()/Date.now()/PROCESS_* 换成显式入参，使输出可复现。
'use strict';
const crypto = require('node:crypto');
const sm3 = require('E:/github/hongguo-open-player/src/server/native/sm3.js');

const u32 = (v) => Number(v) >>> 0;
const rol32 = (v, c) => { const n = c & 31; const x = u32(v); return u32((x << n) | (x >>> (32 - n))); };
const ror32 = (v, c) => { const n = c & 31; const x = u32(v); return u32((x >>> n) | (x << (32 - n))); };
const le32 = (v) => { const b = Buffer.alloc(4); b.writeUInt32LE(u32(v)); return b; };
const zigzag = (v) => { const n = BigInt(v); return n < 0n ? ((-n) * 2n - 1n) : n * 2n; };
const varint = (v) => { let n = BigInt(v); const out = []; while (n > 127n) { out.push(Number((n & 127n) | 128n)); n >>= 7n; } out.push(Number(n)); return Buffer.from(out); };
const md5 = (d) => crypto.createHash('md5').update(d).digest();

// —— 通用 protobuf 组包（源实现 proto/protoField）——
function protoField(tag, value, type = 'string') {
  if (value === undefined || value === null || value === '') return Buffer.alloc(0);
  if (type === 'bytes' || type === 'string' || type === 'message') {
    const body = type === 'bytes' ? Buffer.from(value) : type === 'message' ? value : Buffer.from(String(value));
    return Buffer.concat([varint((BigInt(tag) << 3n) | 2n), varint(body.length), body]);
  }
  if (type === 'float') { const body = Buffer.alloc(4); body.writeFloatLE(Number(value)); return Buffer.concat([varint((BigInt(tag) << 3n) | 5n), body]); }
  return Buffer.concat([varint(BigInt(tag) << 3n), varint(type === 'sint' ? zigzag(value) : value)]);
}
const proto = (fields) => Buffer.concat(fields.map(([tag, value, type]) => protoField(tag, value, type)));

// —— f13 链 ——
function getIv(iv, data) { let value = u32(iv); for (let i = 0; i < data.length; i += 1) { if ((i & 1) === 0) value = u32((value >>> 4) ^ value ^ (value << 6) ^ data[i]); else value = u32(~((value >>> 7) ^ value ^ (data[i] | (value << 12)))); } return value; }
function sumMd5(data) { let check = 0x20220420; for (let i = 0; i < 12; i += 1) { const temp = (i & 1) === 0 ? (check >>> 3) ^ check : (check >>> 5) ^ check; check = (i & 1) === 0 ? data[i] ^ (check << 7) : data[i] | (check << 11); if ((i & 1) !== 0) check = ~check; check = u32(check ^ temp); } return u32((check | 4) ^ 0x1000000); }
const BRANCH2_SV = [0xa7aefe20,0x7149f1d6,0x47e4ca07,0xe9b58f67,0x93b924de,0xc614d0f5,0x38afe0ef,0xb2bbad73,0xe24444c3,0x9d3aec9b,0xdf7b37e4,0xd8b16d40,0xf8ac31b8,0x76b9a90b,0x31d833ee,0x953fce64,0x353595a4,0x4609c13b,0x36925008,0x8c6d0925,0x5df5c177,0x1cfbf52b,0x8a4fa7f0,0x114ca35e,0x8193f984,0x7a7a8733,0x316ab4d5,0x3c20cfc9,0xa6d84453,0x3a18500c,0x798ec47a,0x97a76b28,0x66c4ff96,0x51716443,0xdd2fc3b,0xb5696da7,0xbbeb3ac5,0x5c53d204,0xd32608ce,0x7279b9ec,0xf4188ecf,0xf7d793db,0x332cc491,0xab76ae15,0x9bebe727,0x18a01384,0x5be9f8a7,0x5f90a754,0x39b663c0,0x36673c83,0x7c92f514,0x9d7d94d7,0xe2e8d9aa,0x5f7e9ea9,0x7abd4551,0x569e05da,0x40a25632,0x3df5a9a5,0xbab37d80,0x454286dc,0x3f5d4e78,0x3d7b75d,0xb1fe4af7,0xa5ab26a3];
const BRANCH2_ORDERS = Buffer.from('0f0704000908030a060b050d0e010c020f05080c0009020103070e060b0a0d04060500070c000a04080f010b0d09020e060b02050403080101070a000d0c090e0d070e0f0b0208030c0509010004060a0d0902060f0b0a040807000c0503010e0c090f07060f030e020d0405010b0a000c050a090e0802040407030f01060b000b0f0408020a0700090d06010e03050c0b060a0508020c03070f0e090d0001040906080f050800040a0b030d01020c0e090d0c0604070a03030f00080105020e01000d0f090a0b0e0402080703060c0501080a0c0f0905060b000304020e070d04080f000c0f0e0d0a01060207090503040205080d0b0a0606000e0f070c09030a08040f000b01060d0c0709030e05020a070b050f00020e0108030d0c0609040d070f08050f06040b0a0e0c090002030d0c020704010b0e0e08060f09050003','hex');
function md5V3Step(kind, a, b, c, d, m, shift, constant) { let f; if (kind === 0) f = (b & c) | ((~b) & d); else if (kind === 1) f = (b & d) | (c & (~d)); else if (kind === 2) f = b ^ c ^ d; else f = c ^ (b | (~d)); return u32(b + rol32(u32(a + f + m + constant), shift)); }
function md5SumV3(message, countV2, orders, countV1) {
  const sv = BRANCH2_SV.map((value) => ror32(value, countV1)); const start = [ror32(0x79e0f2fb, countV2), ror32(0xc8b52570, countV2), ror32(0xebc2f8cd, countV2), ror32(0x7c104d93, countV2)]; const endCount = (countV2 + 6) & 255; const end = [ror32(0x19be4866, endCount), ror32(0xe85986b4, endCount), ror32(0xe19b326e, endCount), ror32(0x71d1d7d4, endCount)]; const m = []; for (let i = 0; i < 16; i += 1) m.push(message.readUInt32LE(i * 4)); let [a,b,c,d] = start;
  const run = (kind, order, shifts, offset) => { for (let i = 0; i < 16; i += 1) { const phase = i & 3; const next = phase === 0 ? md5V3Step(kind,a,b,c,d,m[orders[offset + i]],shifts[i],sv[offset + i]) : phase === 1 ? md5V3Step(kind,d,a,b,c,m[orders[offset + i]],shifts[i],sv[offset + i]) : phase === 2 ? md5V3Step(kind,c,d,a,b,m[orders[offset + i]],shifts[i],sv[offset + i]) : md5V3Step(kind,b,c,d,a,m[orders[offset + i]],shifts[i],sv[offset + i]); if (phase === 0) a = next; else if (phase === 1) d = next; else if (phase === 2) c = next; else b = next; } };
  run(0, orders, [7,12,17,22,7,12,17,22,7,12,17,22,7,12,17,22], 0);
  run(1, orders, [5,9,14,20,5,9,14,20,5,9,14,20,5,9,14,20], 16);
  run(2, orders, [4,11,16,23,4,11,16,23,4,11,16,23,4,11,16,23], 32);
  run(3, orders, [6,10,15,21,6,10,15,21,6,10,15,21,6,10,15,21], 48);
  const ret = Buffer.alloc(16); ret.writeUInt32LE(u32((start[0] + a) ^ end[0]), 0); ret.writeUInt32LE(u32((start[1] + b) ^ end[1]), 4); ret.writeUInt32LE(u32((start[2] + c) ^ end[2]), 8); ret.writeUInt32LE(u32((start[3] + d) ^ end[3]), 12); return Buffer.concat([ret, le32(sumMd5(ret))]);
}
function branch2F13(iv, querySm3, bodyMd5, tsBytes, khronos) { const v = (iv & 13) * 86; const ivV0 = ((v >>> 15) & 255) + ((v >>> 8) & 255); const n1 = [0x8980f29b,0xeb549c7f,0xb08726db,0xd40cb5e6,0xe8f559e4][ivV0]; const countV1 = (n1 + khronos + 1) & 255; const countV2 = u32(n1 + khronos); const shift = (countV2 + 5) & 7; const seed = [0x84,0x96,0x77,0x9d,0xd4,0x15,0x0b,0xf8]; const pad = Buffer.from(seed.map((value) => ((value | (value << 8)) >> shift) & 255)); const input = Buffer.concat([querySm3,bodyMd5,tsBytes,pad,Buffer.from('a0010000','hex')]); return md5SumV3(input,countV2,BRANCH2_ORDERS.subarray(ivV0 << 6,(ivV0 + 1) << 6),countV1); }
function bxor(a, b) { const out = Buffer.alloc(Math.min(a.length, b.length)); for (let i = 0; i < out.length; i += 1) out[i] = a[i] ^ b[i]; return out; }
function hashF13(querySm3, bodyMd5, tsBytes, khronos) {
  const iv = getIv(getIv(getIv(0x20230928, querySm3), bodyMd5), tsBytes); const low = iv & 15; const ivV0 = (low * 171) >> 9; const branch = low - ivV0 * 3;
  if (branch === 2) return branch2F13(iv, querySm3, bodyMd5, tsBytes, khronos);
  if (branch !== 0) return Buffer.concat([querySm3.subarray(0, 16), bodyMd5, le32(sumMd5(Buffer.concat([querySm3, bodyMd5]))) ]);
  const ivV1List = [0xc4a78580, 0xb3c0fd39, 0xc58c5686, 0xc9aa3ba7, 0xf5a7adf2, 0x963c2ed1]; const ivV1 = ivV1List[ivV0]; const countV1 = (ivV1 + khronos + 1) & 255; const countV2 = u32(ivV1 + khronos);
  const tt02 = [0xebb64faf,0x7aadcc2,0xcf3187bf,0xe01138ff,0x6d0bfcff,0x5a30a3be,0xb41ad638,0x34180eb8,0xf233eb6f,0xb1a584cc,0xccc30dc7,0x47d1db51,0xd55653de,0x70a84fa1,0x57473c12,0xf76f0288,0x2c077f0a,0xda0dcad0,0xfbb86f6c,0xfdc4cf00,0x688a020d,0xe676c6a6,0x8cd6338b,0x1a3c8d0e,0xcce8b06b,0x6ad0ed0b,0xa0522717,0xdc71ac83,0x2285db71,0xd5b4dda6,0x736f8650,0x6560306c,0x617ce2a6,0xe423417e,0xa40e143,0x544e4032,0x88dffb2a,0x716c1ae0,0x4c467a88,0x5b23bb3,0xe1d0b866,0xbaa3dcb8,0xae3374d3,0xc3381a50,0x1702f75b,0xfe6da368,0xf0b4cf48,0x4e0ffbb8,0x72aad10d,0x26c53a3d,0xf2bce0f6,0xb4557581,0x4a257fdd,0x8c3182a2,0xab0b3b86,0x3d5dfb14,0x4f103634,0xd37b52d7,0x444eff16,0xeb0a33d1,0x6ca86f6e,0x284ba7,0x8387cfa,0x5fb37586];
  const tt = tt02.map((value) => ror32(value, countV1)); const n0 = (countV2 + 2) & 7; const seed = [0xfa,0x45,0x61,0xd7]; const pad = Buffer.from(seed.map((value) => ((value | (value << 8)) >> n0) & 255));
  const data = Buffer.concat([querySm3, bodyMd5, tsBytes, pad, Buffer.from('00000000000001a0','hex')]); const di = []; for (let i = 0; i < data.length; i += 4) di.push(data.readUInt32BE(i)); let di0 = di[0]; for (let i = 0; i < 112; i += 1) { const di1 = di[i + 1]; const di14 = di[i + 14]; const r1 = rol32(di1, 14) ^ rol32(di1, 25) ^ (di1 >>> 3); const r2 = rol32(di14, 13) ^ rol32(di14, 15) ^ (di14 >>> 10); di.push(u32(di0 + di[i + 9] + r1 + r2)); di0 = di1; }
  const init = [0x7aba4fc8,0x67166507,0x6403fa00,0x340f512f,984304912,3005047866,2874125293,2152413264].map((v) => ror32(v, countV2 & 31)); const chosen = [[101,5,7,6,3,2,1,0,5,4,3],[96,0,6,7,5,3,2,1,5,4,4],[96,7,6,2,1,4,0,5,4,3,5],[99,3,6,2,4,5,1,0,0,7,6],[96,0,5,6,7,3,1,2,5,4,4],[100,2,0,3,5,4,6,7,2,1,5]][ivV0]; let d = init.slice(); for (let i = 0; i < chosen[0]; i += 1) { const base = ivV1 + i; const n0v = di[base & 127]; const n1 = ((d[chosen[3]] ^ d[chosen[4]]) & d[chosen[1]]) ^ d[chosen[3]]; const n2 = rol32(d[chosen[1]],26)^rol32(d[chosen[1]],21)^rol32(d[chosen[1]],7); const n4 = u32(n0v+n1+n2+tt[base&63]+d[chosen[5]]); const n5=rol32(d[chosen[2]],30)^rol32(d[chosen[2]],19)^rol32(d[chosen[2]],10); const n6=(d[chosen[2]]&d[chosen[6]])|((d[chosen[2]]|d[chosen[6]])&d[chosen[7]]); const n7=u32(n5+n6); const old=d[chosen[9]]; d.unshift(d.pop()); d[chosen[10]]=u32(n7+n4); d[chosen[8]]=u32(old+n4); }
  const ret = Buffer.alloc(32); for (let i = 0; i < 8; i += 1) ret.writeUInt32BE(u32(d[i]+init[i]), i*4); const folded = bxor(ret.subarray(0,16), ret.subarray(16)); folded.copy(ret,0); return Buffer.concat([ret.subarray(0,16), le32(sumMd5(ret.subarray(0,16)))]);
}

// —— AesV3 ——
const SBOX = Buffer.from('+n0Ia5xZs0sEXznQOEqRmQBnpiCf9U2Ccybu3xhmgzOAAxn72f6uqqmwUsYL83klTni0NqxdGieeiNu9PGPsSRXBMB/cuFbUbM3KCUPINaPvHvSW0vwOcnuUhNHqRVpiAj/TEoE0K91+5ijypUYTATsh9mE3KSoN7Yyvv51cuyR2D3XkU4nhmI2xmmVwT1RMWKtub4sjxAcRDLrPoKSO2AU9FLLadMPX577Wf95IFj6FkKFVt3dCIsmGUC4X+WQxLJvxbRxEaOPpqJOXyzJX6+Vxaq3AzMfF/WAdoi1Hp+JRaV56zgpBtpWP97mH4DoGEIq1+FvV8LyS/3wvwugbQOwb2r26mJEMsiuDQTRn+wrYdrVGBVlhI3WQhyrjUBVMrLF5667llUcEaPCGPVGLD8qO5LlO8hKCvA7V9+8oJc9bXelqVQLhM76T5/WtnT45JKji+hdX0HoNCDDWuKON/QeaxB5uImSX0h2wv0VmP2zd2yeApxHcpsVS+MC2yFwAc2B7oBkTqsk1SEvTpM2fmfMQREBUfin0Bh+iq6EvPPavhWI2IX9e3yAas7Tm/3KEj2UmlFp36kN4x0rMLBRrxuh0U/zUHM4xcAMYjJY4MonxOl/X+alpt2M3WMI7w3HLnpIBigtNiJu7T21v4P6lSd5WFgntnMEt7oF92XzRLkJbTcGmXepE/UVOG6E/0YnhfS+q26utWcuxzpooyeD2cDlK1/8w9d28VzsRjbLuALbmGlp8+d7EzS6Au7lMpZ+ECMZvQmzwJ+eLOpxR+2chdUExp8ogQyq3v9l68rX4jCwjg0+PYKAEEzcU4wHFY2ZcdIHfWL1okD3SszT0GZMyKdZJrg1L2AeerB4tC0C4crp2EHGo5FYdSP7lwkeR2ocmnR+Ia8CYviUJlzOjhRZef9xuVOn3qcjow3fQgivsAmKKkg4+sA8F8/GWeDiGNhg8JM8KtFPMYWWkx5TVFX5t73kiNRJqjlIGVXtGZFCV4gzt0xcDopuZ6xz8r9Rzafpf9ywev8jh8592gHFIqpStZPuJxmDDMrNN0uBE3V+oscdoIzTJbRJ/t+sVvqnReJOgDJKk10fjisJwqyZBmnmn2BSFj8BvVtCMEbkuPOKdzw7eA11GPs04Qw8zWtkaZWwiO/wwpojqN6K0jY5RnNZA7vn4hPSul+nKCkVnVwQvg1zVxcSCtqORmB9KrJaBbssbCQivGJVJfVTt+hYxOtq4ZvWl8f4QAQZ0zGPffCgl9s6yT4vlvIdpu4YhBwA25wtQWZsc6GJYGWHyvSdeuh3mmUI9DSq13Fsp8C1MU3tqc04/df9LoTUXVXI5INOw/e8C7Hd+5CvbkMEFnnrUUmskEw==','base64');
function xorBytes(a, b) { const out = Buffer.from(a); for (let i = 0; i < out.length; i += 1) out[i] ^= b[i % b.length]; return out; }
function matrix(bytes) { return Array.from({ length: 4 }, (_v, i) => Array.from(bytes.subarray(i * 4, i * 4 + 4))); }
function flat(rows) { return Buffer.from(rows.flat()); }
function aesMix(rows) { for (let i = 0; i < 4; i += 1) { const t=rows[0][i]^rows[1][i]^rows[2][i]^rows[3][i]; const u=rows[0][i]; const xt=(x)=>((x<<1)^((x&128)?0x1b:0))&255; rows[0][i]^=t^xt(rows[0][i]^rows[1][i]); rows[1][i]^=t^xt(rows[1][i]^rows[2][i]); rows[2][i]^=t^xt(rows[2][i]^rows[3][i]); rows[3][i]^=t^xt(rows[3][i]^u); } }
class AesV3 {
  constructor(key, khronos) { this.wordSize=khronos&3; this.box=SBOX.subarray(this.wordSize*256,(this.wordSize+1)*256); this.con=[[1,0,2,3],[1,3,0,2],[0,1,3,2],[1,0,2,3]][this.wordSize]; this.con2=[[1,0,2,3],[2,0,3,1],[0,1,3,2],[1,0,2,3]][this.wordSize]; this.order=[[0,9,14,11,4,13,2,7,8,1,6,15,12,5,10,3],[0,9,14,15,4,13,2,7,8,1,6,3,12,5,10,11],[0,9,14,7,4,13,2,11,8,1,6,3,12,5,10,15],[0,9,14,11,4,13,2,7,8,1,6,15,12,5,10,3]][this.wordSize]; const init=[0xca025ddc,0x823dc546,0xc9420583,0xc298225f][this.wordSize]; const initial=Buffer.alloc(16); for(let i=0;i<4;i+=1)initial.writeUInt32LE(init,i*4); const mk=xorBytes(initial,key); const expanded=Buffer.concat([mk,Buffer.alloc(32)]); let rounds=8; for(let i=4;i<12;i+=1){let at=4*(i-1);let a=expanded[at],b=expanded[at+1],c=expanded[at+2],d=expanded[at+3];if((i&3)===0){const t=(u32(init>>((rounds&24)))^this.box[b])&255;b=this.box[c];c=this.box[d];d=this.box[a];a=t;}rounds+=2;expanded[at+4]=a^expanded[at-12];expanded[at+5]=b^expanded[at-11];expanded[at+6]=c^expanded[at-10];expanded[at+7]=d^expanded[at-9];} this.keys=Array.from({length:12},(_v,i)=>Array.from(expanded.subarray(i*4,i*4+4))); }
  add(rows,key) { for(let i=0;i<4;i+=1) for(let j=0;j<4;j+=1) rows[i][j]^=key[i][j]; }
  addCon(rows, keys) { for(let i=0;i<4;i+=1) for(let j=0;j<4;j+=1) rows[i][j]^=keys[i][this.con2[j]]; }
  sub(rows) { for(let i=0;i<4;i+=1) for(let j=0;j<4;j+=1) rows[i][j]=this.box[rows[i][j]]; const old=rows.map((x)=>x.slice()); for(let i=0;i<4;i+=1) rows[i]=old[this.con2[i]]; }
  shift(rows) { const b=flat(rows); for(let i=0;i<16;i+=1) rows[Math.floor(i/4)][i%4]=b[this.order[i]]; }
  shiftCon(rows) { const old=rows.map((x)=>x.slice()); for(let i=0;i<4;i+=1) rows[i]=this.con2.map((x)=>old[i][x]); }
  block(value) { const rows=matrix(value); this.addCon(rows,this.keys.slice(0,4)); for(let i=1;i<3;i+=1){this.sub(rows);this.shift(rows);if(i===1){this.shiftCon(rows);aesMix(rows);}this.addCon(rows,this.keys.slice(i*4));}this.add(rows,this.keys.slice(4));return flat(rows); }
  encrypt(data, iv) { const source=Buffer.from(data); const plaintext=Buffer.alloc(32); for(let i=0;i<31;i+=1){const at=i*8;const n0=(source[at]>>4)&2;const n1=n0|(source[at+1]&64);const n2=n1|((source[at+2]>>2)&1);const n3=n2|((source[at+3]<<3)&128);const n4=n3|((source[at+4]>>1)&4);const n5=n4|((source[at+5]<<3)&16);const n6=n5|((source[at+6]<<5)&32);plaintext[i]=n6|((source[at+7]>>4)&8);} plaintext[31]=1; const blocks=[]; let previous=Buffer.from(iv); for(let i=0;i<plaintext.length;i+=16){const value=this.block(xorBytes(plaintext.subarray(i,i+16),previous));blocks.push(value);previous=value;} const key=Buffer.concat(blocks); const out=Buffer.from(source); for(let i=0;i<31;i+=1){const at=i*8; const k=key[i]; out[at]&=0xdf;out[at]|=(k<<4)&32;out[at+1]&=0xbf;out[at+1]|=k&64;out[at+2]&=0xfb;out[at+2]|=(k<<2)&4;out[at+3]&=0xef;out[at+3]|=(k>>3)&16;out[at+4]&=0xf7;out[at+4]|=(k+k)&8;out[at+5]&=0xfd;out[at+5]|=(k>>3)&2;out[at+6]&=0xfe;out[at+6]|=(k>>5)&1;out[at+7]&=0x7f;out[at+7]|=(k<<4)&128;} return Buffer.concat([key.subarray(-1),out]); }
}
function xmxor(data, key) { const encoded=Buffer.alloc(data.length); for(let i=0;i<data.length;i+=1){const at=(i*4)&28;const d0=key[at],d1=key[at+1];let d2=((((data[i]<<4)|(data[i]>>>4))&255)+d0);d2=(~d2)^d1;d2=((((d2&255)<<3)|((d2&255)>>>5))&255);d2=(d2+d1)&255;d2=(d2^d0)&255;encoded[data.length-i-1]=(~d2)&255;} let last=encoded[encoded.length-1]^encoded[encoded.length-2];const first=encoded[0];encoded[0]=(~last+first)&255;encoded[1]=((encoded[0]^encoded[encoded.length-1]^254)+encoded[1])&255;encoded[2]=(encoded[2]+((last-first)^(((encoded[1]<<3)|(encoded[1]>>>5))&255)^2))&255;for(let i=0;i<encoded.length-4;i+=1){const temp=(((encoded[i+2]<<3)|(encoded[i+2]>>>5))&255)^encoded[i+1]^(i+3);encoded[i+3]=(~temp+encoded[i+3])&255;}encoded[encoded.length-1]^=encoded[encoded.length-2];let sum=0;for(let i=0;i<encoded.length-1;i+=1)sum+=encoded[i+1];encoded[0]=((encoded[0]^encoded[1])+sum)&255;return encoded; }
function keyHash(signKey, random) { const input=Buffer.concat([signKey,le32(random),signKey]); const hash=sm3(input); const d1=(random>>16)&255; let d2=((d1<<11)|(random>>>24))^(d1>>5)^d1; d2=(~d2)>>>0; return [hash,le32(d2)]; }

// —— 设备指纹 protobuf（注入 nonce/started）——
function deviceProto(deviceId, nonce, started) { return proto([[1,1,'sint'],[2,2,'sint'],[3,'8662'],[4,deviceId],[5,nonce],[6,'!noperm!'],[7,-888888,'sint'],[8,-888888,'sint'],[9,3,'sint'],[10,-888888,'sint'],[11,'!notset!'],[12,'Asia/Shanghai,8'],[13,'zh_CN'],[14,4,'sint'],[16,0,'float'],[17,0,'float'],[18,0,'float'],[19,0,'float'],[20,0,'float'],[21,0,'float'],[22,'16'],[23,41,'sint'],[24,36,'sint'],[25,started,'sint'],[26,started,'sint'],[27,started,'sint'],[28,started,'sint'],[29,-1,'sint'],[30,'25053RT47C'],[31,'Redmi'],[32,'25053RT47C'],[33,'25053RT47C'],[34,'Xiaomi'],[35,'Redmi'],[36,'Redmi'],[38,31,'sint']]); }

// —— buildMedusa（注入全部随机与时间）——
function buildMedusa(url, body, khronos, device, R) {
  const bodyMd5=body?md5(body):Buffer.alloc(16); const querySm3=sm3(url.split('?')[1]); const ts=le32(khronos); const queryBodyTs=hashF13(querySm3,bodyMd5,ts,khronos);
  const nested=proto([[1,111,'sint'],[2,10,'sint'],[3,694367,'sint'],[5,586952199,'sint']]);
  const env=proto([[1,R.envLaunch,'sint'],[2,146331399,'sint'],[3,146331396,'sint'],[5,7,'sint'],[6,'v04.06.04.03-bugfix'],[7,R.envPid,'sint'],[12,deviceProto(device.device_id,R.nonce,R.started),'message'],[13,proto([[1,R.nowSec,'sint'],[2,-2,'sint'],[4,200,'sint']]),'message'],[14,device.version_name]]);
  const queryHash=sm3(Buffer.concat([Buffer.from(url.split('?')[1]),bodyMd5,Buffer.from('none')]));
  const message=proto([[1,Buffer.from('f7e85ffad7d7dc3bd62ac87057cf6118','hex'),'bytes'],[2,3,'sint'],[3,R.messageRand,'sint'],[4,'8662'],[5,device.device_id],[6,'1588093228'],[7,device.version_name],[8,'v04.06.04-ml-android'],[9,67503104,'sint'],[10,Buffer.from('4001000000000000','hex'),'bytes'],[12,khronos,'sint'],[13,queryBodyTs,'bytes'],[14,querySm3.subarray(0,6),'bytes'],[15,nested,'message'],[16,R.nonce],[17,khronos,'sint'],[19,queryHash,'bytes'],[20,'none'],[21,312,'sint'],[23,env,'message'],[24,'{"cmr":16777216,"cmr2":16777216,"un_h":1879194040,"vpn":0,"kd":0,"fkd":3672518972,"pd":-1872573247,"dyn":"","do":0,"tk":true}']]);
  const [hash,seed]=keyHash(Buffer.from('8ebdfa3806ecc5cee79423e6029ed82540bc2218bb7eaef71cb691f7aa8aa2f5','hex'),R.random);
  let transformed=xmxor(message,hash); transformed=Buffer.concat([Buffer.from('4001000000000000','hex'),transformed]).reverse(); for(let i=0;i<transformed.length;i+=1)transformed[i]^=seed[(~i)&3];
  const check=u32(((querySm3[0]&63)<<14)|0x18000001|((queryBodyTs[0]&63)<<8));
  const packed=Buffer.concat([Buffer.from([0x35]),le32(R.xmRand),le32(check),transformed,Buffer.from([R.random>>16,R.random>>24])]);
  const encrypted=new AesV3(Buffer.from('f1593376766ea98d34f31b057a9d5be4','hex'),khronos).encrypt(packed,Buffer.from('1fe109a4125283f418de9e051a969e12','hex'));
  const version=Buffer.from('03000000f7e85ffad7d7dc3bd62ac87057cf6118','hex'); const prefix=Buffer.alloc(20); for(let i=0;i<20;i+=4) prefix.writeUInt32LE(u32(version.readUInt32LE(i)^khronos),i);
  return Buffer.concat([prefix,Buffer.from([R.random&255,(R.random>>8)&255,0,1]),encrypted]).toString('base64');
}

const R = { nonce: 'AAAAAAAAAAAAAAAAAAAAAAAA', started: 1700000000000, messageRand: 0x11223344, envLaunch: 5, envPid: 6789, nowSec: 1700000000, random: 0x55667788, xmRand: 0x99aabbcc };
const device = { device_id: '1624456208323876', version_name: '7.3.5.32' };
const url = 'https://api5-normal-sinfonlineb.fqnovel.com/reading/distribution/category/landpage/v/?aid=8662&ts=1700000000';
const body = Buffer.from('{"limit":15,"req_scene":"default"}');
console.log(JSON.stringify({ medusa: buildMedusa(url, body, 1700000000, device, R) }));
