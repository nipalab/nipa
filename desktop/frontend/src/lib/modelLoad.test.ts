import { AnimationClip, Bone, BoxGeometry, BufferAttribute, Group, Mesh, MeshPhongMaterial, MeshStandardMaterial, Texture } from "three";
import { FBXLoader } from "three/examples/jsm/loaders/FBXLoader.js";
import { MTLLoader } from "three/examples/jsm/loaders/MTLLoader.js";
import { USDLoader } from "three/examples/jsm/loaders/USDLoader.js";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ModelSide } from "@/api/desktop";
import { fetchBytes, mediaExpired } from "./media";
import { cleanPath, clipName, fileManager, loadModel, mimeFor, sceneStats } from "./modelLoad";

const toB64 = (bytes: Uint8Array) => btoa(String.fromCharCode(...bytes));
const textB64 = (s: string) => toB64(new TextEncoder().encode(s));
const dataURL = (b64: string) => `data:application/octet-stream;base64,${b64}`;
const textURL = (s: string) => dataURL(textB64(s));
const binURL = (b: Uint8Array) => dataURL(toB64(b));
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

const side = (over: Partial<ModelSide>): ModelSide => ({ path: "m", format: "gltf", size: 0, stats: null, url: "", media: "", tokens: [], files: {}, missing: [], ...over });

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
  it("guesses mime types", () => {
    expect(mimeFor("a/B.PNG")).toBe("image/png");
    expect(mimeFor("x.bin")).toBe("application/octet-stream");
    expect(mimeFor("noext")).toBe("application/octet-stream");
    expect(clipName(new AnimationClip("", 1, []), 2)).toBe("Animation 3");
  });

  it("points referenced files at their media URLs", () => {
    const m = fileManager({ "tex/a b.png": "/nipa-media/t1/a b.png", "x.bin": "/nipa-media/t2/x.bin" });
    expect(m.resolveURL("tex/a%20b.png")).toBe("/nipa-media/t1/a b.png");
    expect(m.resolveURL("./x.bin")).toBe("/nipa-media/t2/x.bin");
    expect(m.resolveURL("sub/../x.bin")).toBe("/nipa-media/t2/x.bin");
    const dotted = fileManager({ "./hero.bin": "/nipa-media/h/hero.bin", "materials/wood.png": "/nipa-media/w/wood.png", "./buffers/scene.bin": "/nipa-media/s/scene.bin" });
    for (const url of ["./buffers/scene.bin", "buffers/scene.bin", "buffers/./scene.bin"]) {
      expect(dotted.resolveURL(url)).toBe("/nipa-media/s/scene.bin");
    }
    expect(dotted.resolveURL("hero.bin")).toBe("/nipa-media/h/hero.bin");
    expect(dotted.resolveURL("materials/./wood.png")).toBe("/nipa-media/w/wood.png");
    expect(m.resolveURL("data:image/png;base64,AA==")).toBe("data:image/png;base64,AA==");
    expect(m.resolveURL("blob:http://app/1")).toBe("blob:http://app/1");
    expect(m.resolveURL("/nipa-media/t1/a b.png")).toBe("/nipa-media/t1/a b.png");
    for (const url of ["other.png", "https://attacker.example/beacon.png", "//attacker.example/a.jpg"]) {
      expect(m.resolveURL(url)).toMatch(/^data:image\/png;base64,iVBOR/);
    }
    expect(m.resolveURL("bad%zz")).toBe("data:application/octet-stream;base64,");
    expect(m.resolveURL("https://attacker.example/buf.bin")).toBe("data:application/octet-stream;base64,");
    expect(m.resolveURL("a.ktx2")).toBe("data:application/octet-stream;base64,");
  });

  it("fetches media bytes with the abort signal and reports a missing file", async () => {
    const buf = new ArrayBuffer(3);
    const fetcher = vi.fn().mockResolvedValue({ ok: true, status: 200, arrayBuffer: () => Promise.resolve(buf) });
    const signal = new AbortController().signal;
    expect(await fetchBytes("/nipa-media/t/a.glb", signal, fetcher)).toBe(buf);
    expect(fetcher).toHaveBeenCalledWith("/nipa-media/t/a.glb", { signal });
    const gone = vi.fn().mockResolvedValue({ ok: false, status: 404, arrayBuffer: () => Promise.reject(new Error("unused")) });
    await expect(fetchBytes("/nipa-media/t/a.glb", undefined, gone)).rejects.toThrow("HTTP 404");
    await expect(loadModel(side({ format: "glb", url: "/nipa-media/t/a.glb" }), undefined, gone)).rejects.toThrow("HTTP 404");
  });

  it("tells an evicted media token from a live one", async () => {
    const has = vi.fn(async (t: string) => t !== "gone");
    expect(await mediaExpired(["a", null, "", undefined], has)).toBe(false);
    expect(await mediaExpired(["a", "gone"], has)).toBe(true);
    expect(await mediaExpired([], has)).toBe(false);
    expect(await mediaExpired(["a"], () => Promise.reject(new Error("no bridge")))).toBe(false);
  });

  it("cleans relative paths", () => {
    expect(cleanPath("./a/./b//c")).toBe("a/b/c");
    expect(cleanPath("a/../../b")).toBe("../b");
    expect(cleanPath("../../x")).toBe("../../x");
  });
});

describe("loadModel", () => {
  it("loads glTF with external buffers and GLB", async () => {
    const gltf = await loadModel(side({ format: "gltf", url: textURL(gltfJSON("tri.bin")), files: { "tri.bin": binURL(triangle) } }));
    const stats = sceneStats(gltf);
    expect(stats.vertices).toBe(3);
    expect(stats.triangles).toBe(1);
    expect(stats.materials).toEqual(["Skin"]);
    expect(stats.max).toEqual([2, 3, 0]);
    expect(gltf.animations.map((a) => a.name)).toEqual(["Spin"]);

    const bin = await loadModel(side({ format: "glb", url: binURL(glb(gltfJSON(""), triangle)) }));
    expect(sceneStats(bin).meshes).toBe(1);
  });

  it("loads a glTF whose buffer path starts with ./ and never fetches unknown files", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const model = await loadModel(side({ url: textURL(gltfJSON("./hero.bin")), files: { "./hero.bin": binURL(triangle) } }));
    expect(sceneStats(model).vertices).toBe(3);
    await expect(loadModel(side({ url: textURL(gltfJSON("https://attacker.example/hero.bin")) }))).rejects.toThrow();
    const urls = fetchSpy.mock.calls.map(([req]) => (req instanceof Request ? req.url : String(req)));
    expect(urls.length).toBeGreaterThan(0);
    expect(urls.every((u) => u.startsWith("data:"))).toBe(true);
  });

  it("loads OBJ with its material library", async () => {
    const obj = "mtllib ship.mtl\no Ship\nv 0 0 0\nv 1 0 0\nv 0 1 0\nusemtl Hull\nf 1 2 3\n";
    const model = await loadModel(side({ format: "obj", url: textURL(obj), files: { "ship.mtl": textURL("newmtl Hull\nKd 1 0 0\n") } }));
    const stats = sceneStats(model);
    expect(stats.materials).toEqual(["Hull"]);
    expect(stats.triangles).toBe(1);
    const parse = vi.spyOn(MTLLoader.prototype, "parse");
    const nested = obj.replace("ship.mtl", "materials/ship.mtl");
    await loadModel(side({ format: "obj", url: textURL(nested), files: { "materials/ship.mtl": textURL("newmtl Hull\nKd 1 0 0\n") } }));
    expect(parse).toHaveBeenLastCalledWith(expect.any(String), "materials/");
    const plain = await loadModel(side({ format: "obj", url: textURL("v 0 0 0\nv 1 0 0\nv 0 1 0\nf 1 2 3\n") }));
    expect(sceneStats(plain).vertices).toBe(3);
  });

  it("applies every OBJ material library", async () => {
    const obj = "mtllib a.mtl b.mtl\r\n  mtllib\tsub/c.mtl absent.mtl \nmtllib\nmtllibx d.mtl\nv 0 0 0\nv 1 0 0\nv 0 1 0\nusemtl A\nf 1 2 3\nusemtl B\nf 1 2 3\nusemtl C\nf 1 2 3\n";
    const model = await loadModel(
      side({
        format: "obj",
        url: textURL(obj),
        files: {
          "a.mtl": textURL("newmtl A\nKd 1 0 0\n"),
          "b.mtl": textURL("newmtl B\nKd 0 1 0\nnewmtl A\nKd 0 0 1\n"),
          "sub/c.mtl": textURL("newmtl C\nKd 0 0 1\n"),
        },
      }),
    );
    const mesh = model.object.children[0] as Mesh;
    const colors = (mesh.material as MeshPhongMaterial[]).map((m) => [m.name, m.color.getHexString()]);
    expect(colors).toEqual([
      ["A", "ff0000"],
      ["B", "00ff00"],
      ["C", "0000ff"],
    ]);
  });

  it("finds OBJ material libraries the way the daemon keys them", async () => {
    const obj = "mtllib ./sub%20dir/a.mtl\nv 0 0 0\nv 1 0 0\nv 0 1 0\nusemtl A\nf 1 2 3\n";
    const parse = vi.spyOn(MTLLoader.prototype, "parse");
    const model = await loadModel(side({ format: "obj", url: textURL(obj), files: { "sub dir/a.mtl": textURL("newmtl A\nKd 1 0 0\n") } }));
    expect(((model.object.children[0] as Mesh).material as MeshPhongMaterial).color.getHexString()).toBe("ff0000");
    expect(parse).toHaveBeenLastCalledWith(expect.any(String), "sub dir/");
  });

  it("hands FBX and USD to their loaders", async () => {
    const fbx = Object.assign(new Group(), { animations: [new AnimationClip("Walk", 1, [])] });
    const parse = vi.spyOn(FBXLoader.prototype, "parse").mockReturnValue(fbx);
    expect((await loadModel(side({ format: "fbx", url: dataURL("AAAA") }))).animations[0].name).toBe("Walk");
    expect(parse.mock.calls[0][0]).toBeInstanceOf(ArrayBuffer);

    const usd = vi.spyOn(USDLoader.prototype, "parse").mockReturnValue(new Group());
    await loadModel(side({ format: "usda", url: textURL("#usda 1.0") }));
    expect(usd.mock.calls[0][0]).toBe("#usda 1.0");
    for (const format of ["usdz", "usdc", "usd"]) await loadModel(side({ format, url: dataURL("AAAA") }));
    expect(usd).toHaveBeenCalledTimes(4);
    await expect(loadModel(side({ format: "blend", url: dataURL("") }))).rejects.toThrow("BLEND models cannot be shown yet");
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
