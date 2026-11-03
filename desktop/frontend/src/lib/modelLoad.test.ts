import { AnimationClip, Bone, BoxGeometry, BufferAttribute, Group, Mesh, MeshStandardMaterial, Texture } from "three";
import { FBXLoader } from "three/examples/jsm/loaders/FBXLoader.js";
import { MTLLoader } from "three/examples/jsm/loaders/MTLLoader.js";
import { USDLoader } from "three/examples/jsm/loaders/USDLoader.js";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ModelSide } from "@/api/desktop";
import { base64ToBytes, clipName, fileManager, loadModel, mimeFor, sceneStats } from "./modelLoad";

const toB64 = (bytes: Uint8Array) => btoa(String.fromCharCode(...bytes));
const textB64 = (s: string) => toB64(new TextEncoder().encode(s));
const triangle = new Uint8Array(new Float32Array([0, 0, 0, 2, 0, 0, 0, 3, 0]).buffer);

const gltfJSON = (bufferUri: string) =>
  JSON.stringify({
    asset: { version: "2.0" },
    scenes: [{ nodes: [0] }],
    scene: 0,
    nodes: [{ mesh: 0, name: "Tri" }],
    meshes: [{ primitives: [{ attributes: { POSITION: 0 }, material: 0 }] }],
    materials: [{ name: "Skin" }],
    accessors: [{ bufferView: 0, componentType: 5126, count: 3, type: "VEC3", min: [0, 0, 0], max: [2, 3, 0] }],
    bufferViews: [{ buffer: 0, byteLength: 36 }],
    buffers: [bufferUri ? { uri: bufferUri, byteLength: 36 } : { byteLength: 36 }],
    animations: [
      {
        name: "Spin",
        samplers: [{ input: 0, output: 0, interpolation: "LINEAR" }],
        channels: [],
      },
    ],
  });

const side = (over: Partial<ModelSide>): ModelSide => ({ path: "m", format: "gltf", size: 0, stats: null, data: "", files: {}, missing: [], ...over });

function glb(json: string, bin: Uint8Array): Uint8Array {
  const pad = (b: Uint8Array, fill: number) => {
    const out = new Uint8Array(Math.ceil(b.length / 4) * 4).fill(fill);
    out.set(b);
    return out;
  };
  const j = pad(new TextEncoder().encode(json), 0x20);
  const b = pad(bin, 0);
  const out = new Uint8Array(12 + 8 + j.length + 8 + b.length);
  const dv = new DataView(out.buffer);
  dv.setUint32(0, 0x46546c67, true);
  dv.setUint32(4, 2, true);
  dv.setUint32(8, out.length, true);
  dv.setUint32(12, j.length, true);
  dv.setUint32(16, 0x4e4f534a, true);
  out.set(j, 20);
  dv.setUint32(20 + j.length, b.length, true);
  dv.setUint32(24 + j.length, 0x004e4942, true);
  out.set(b, 28 + j.length);
  return out;
}

afterEach(() => vi.restoreAllMocks());

describe("helpers", () => {
  it("decodes base64 and guesses mime types", () => {
    expect([...base64ToBytes("AQID")]).toEqual([1, 2, 3]);
    expect(mimeFor("a/B.PNG")).toBe("image/png");
    expect(mimeFor("x.bin")).toBe("application/octet-stream");
    expect(mimeFor("noext")).toBe("application/octet-stream");
    expect(clipName(new AnimationClip("", 1, []), 2)).toBe("Animation 3");
  });

  it("serves referenced files from memory", () => {
    const m = fileManager({ "tex/a b.png": "QQ==", "x.bin": "Qg==" });
    expect(m.resolveURL("tex/a%20b.png")).toBe("data:image/png;base64,QQ==");
    expect(m.resolveURL("./x.bin")).toBe("data:application/octet-stream;base64,Qg==");
    expect(m.resolveURL("other.png")).toBe("other.png");
    expect(m.resolveURL("bad%zz")).toBe("bad%zz");
  });
});

describe("loadModel", () => {
  it("loads glTF with external buffers and GLB", async () => {
    const gltf = await loadModel(side({ format: "gltf", data: textB64(gltfJSON("tri.bin")), files: { "tri.bin": toB64(triangle) } }));
    const stats = sceneStats(gltf);
    expect(stats.vertices).toBe(3);
    expect(stats.triangles).toBe(1);
    expect(stats.materials).toEqual(["Skin"]);
    expect(stats.max).toEqual([2, 3, 0]);
    expect(gltf.animations.map((a) => a.name)).toEqual(["Spin"]);

    const bin = await loadModel(side({ format: "glb", data: toB64(glb(gltfJSON(""), triangle)) }));
    expect(sceneStats(bin).meshes).toBe(1);
  });

  it("loads OBJ with its material library", async () => {
    const obj = "mtllib ship.mtl\no Ship\nv 0 0 0\nv 1 0 0\nv 0 1 0\nusemtl Hull\nf 1 2 3\n";
    const model = await loadModel(side({ format: "obj", data: textB64(obj), files: { "ship.mtl": textB64("newmtl Hull\nKd 1 0 0\n") } }));
    const stats = sceneStats(model);
    expect(stats.materials).toEqual(["Hull"]);
    expect(stats.triangles).toBe(1);
    const parse = vi.spyOn(MTLLoader.prototype, "parse");
    const nested = obj.replace("ship.mtl", "materials/ship.mtl");
    await loadModel(side({ format: "obj", data: textB64(nested), files: { "materials/ship.mtl": textB64("newmtl Hull\nKd 1 0 0\n") } }));
    expect(parse).toHaveBeenLastCalledWith(expect.any(String), "materials/");
    const plain = await loadModel(side({ format: "obj", data: textB64("v 0 0 0\nv 1 0 0\nv 0 1 0\nf 1 2 3\n") }));
    expect(sceneStats(plain).vertices).toBe(3);
  });

  it("hands FBX and USD to their loaders", async () => {
    const fbx = Object.assign(new Group(), { animations: [new AnimationClip("Walk", 1, [])] });
    const parse = vi.spyOn(FBXLoader.prototype, "parse").mockReturnValue(fbx);
    expect((await loadModel(side({ format: "fbx", data: "AAAA" }))).animations[0].name).toBe("Walk");
    expect(parse.mock.calls[0][0]).toBeInstanceOf(ArrayBuffer);

    const usd = vi.spyOn(USDLoader.prototype, "parse").mockReturnValue(new Group());
    await loadModel(side({ format: "usda", data: textB64("#usda 1.0") }));
    expect(usd.mock.calls[0][0]).toBe("#usda 1.0");
    for (const format of ["usdz", "usdc", "usd"]) await loadModel(side({ format, data: "AAAA" }));
    expect(usd).toHaveBeenCalledTimes(4);
    await expect(loadModel(side({ format: "blend", data: "" }))).rejects.toThrow("BLEND models cannot be shown yet");
  });
});

describe("sceneStats", () => {
  it("counts meshes, bones, morph targets and textures", () => {
    const geo = new BoxGeometry();
    geo.morphAttributes.position = [new BufferAttribute(new Float32Array(72), 3)];
    const textured = new MeshStandardMaterial({ map: new Texture() });
    const group = new Group();
    group.add(new Mesh(geo, [textured, new MeshStandardMaterial({ name: "Trim" })]));
    group.add(new Mesh(new BoxGeometry(), textured));
    const unindexed = new BoxGeometry().toNonIndexed();
    group.add(new Mesh(unindexed, new MeshStandardMaterial()));
    group.add(new Bone());
    const stats = sceneStats({ object: group, animations: [new AnimationClip("", 1, [])] });
    expect(stats.meshes).toBe(3);
    expect(stats.vertices).toBe(84);
    expect(stats.triangles).toBe(36);
    expect(stats.morphTargets).toBe(1);
    expect(stats.joints).toBe(1);
    expect(stats.textures).toBe(1);
    expect(stats.materials).toEqual(["Material 1", "Trim", "Material 3"]);
    expect(stats.animations).toEqual(["Animation 1"]);
    expect(stats.min).toEqual([-0.5, -0.5, -0.5]);
    expect(sceneStats({ object: new Group(), animations: [] }).min).toBeNull();
  });
});
