import {
  AmbientLight,
  AnimationMixer,
  Box3,
  Clock,
  Color,
  DirectionalLight,
  GridHelper,
  HemisphereLight,
  MeshBasicMaterial,
  PerspectiveCamera,
  Scene,
  Sphere,
  Vector3,
  WebGLRenderer,
  type Material,
} from "three";
import { OrbitControls } from "three/examples/jsm/controls/OrbitControls.js";
import { clipName, disposeModel, type LoadedModel } from "@/lib/modelLoad";

export type StageMode = "side" | "overlay";
export type Shading = "shaded" | "wireframe" | "both";

// RendererLike is the slice of WebGLRenderer the stage drives, so tests can
// run it without a GPU.
export type RendererLike = Pick<
  WebGLRenderer,
  "domElement" | "setPixelRatio" | "setSize" | "setScissorTest" | "setScissor" | "setViewport" | "render" | "clear" | "clearDepth" | "setAnimationLoop" | "dispose" | "setClearColor"
> & { autoClear: boolean };

export type Stage = {
  setModels(old: LoadedModel | null, next: LoadedModel | null): void;
  setMode(mode: StageMode): void;
  setShading(shading: Shading): void;
  playAnimation(name: string | null): void;
  resetView(): void;
  resize(width: number, height: number): void;
  dispose(): void;
};

type Side = { scene: Scene; model: LoadedModel | null; mixer: AnimationMixer | null };

function sideScene(): Side {
  const scene = new Scene();
  scene.add(new HemisphereLight(0xffffff, 0x444444, 1.6));
  scene.add(new AmbientLight(0xffffff, 0.4));
  const sun = new DirectionalLight(0xffffff, 2);
  sun.position.set(3, 5, 4);
  scene.add(sun);
  return { scene, model: null, mixer: null };
}

const ghostMaterial = new MeshBasicMaterial({ color: new Color("#f97316"), wireframe: true, transparent: true, opacity: 0.45, depthTest: false });
const wireMaterial = new MeshBasicMaterial({ color: new Color("#6366f1"), wireframe: true });
const edgeMaterial = new MeshBasicMaterial({ color: new Color("#0f172a"), wireframe: true, transparent: true, opacity: 0.3 });

export function createStage(container: HTMLElement, makeRenderer: () => RendererLike = () => new WebGLRenderer({ antialias: true, alpha: true })): Stage {
  const renderer = makeRenderer();
  renderer.setPixelRatio(globalThis.devicePixelRatio || 1);
  renderer.setClearColor(0x000000, 0);
  renderer.autoClear = false;
  container.appendChild(renderer.domElement);

  const camera = new PerspectiveCamera(45, 1, 0.01, 1000);
  const controls = new OrbitControls(camera, renderer.domElement);
  controls.enableDamping = true;
  const sides: [Side, Side] = [sideScene(), sideScene()];
  const grid = new GridHelper(10, 10, 0x888888, 0x444444);
  const clock = new Clock();
  let mode: StageMode = "side";
  let shading: Shading = "shaded";
  let width = 1;
  let height = 1;

  const visible = () => sides.filter((s) => s.model);

  const fit = () => {
    const box = new Box3();
    for (const s of visible()) box.expandByObject(s.model!.object);
    if (box.isEmpty()) return;
    const sphere = box.getBoundingSphere(new Sphere());
    const radius = Math.max(sphere.radius, 1e-3);
    const dist = (1.15 * radius) / Math.sin((camera.fov * Math.PI) / 360);
    camera.near = dist / 100;
    camera.far = dist * 100;
    camera.position.copy(sphere.center).add(new Vector3(0.6, 0.45, 1).normalize().multiplyScalar(dist));
    camera.updateProjectionMatrix();
    controls.target.copy(sphere.center);
    controls.update();
    grid.scale.setScalar(radius / 2.5);
    grid.position.set(sphere.center.x, box.min.y, sphere.center.z);
  };

  const renderScene = (scene: Scene, override: Material | null) => {
    scene.overrideMaterial = override;
    renderer.render(scene, camera);
    scene.overrideMaterial = null;
  };

  const renderShaded = (side: Side) => {
    side.scene.add(grid);
    if (shading !== "wireframe") renderScene(side.scene, null);
    if (shading === "wireframe") renderScene(side.scene, wireMaterial);
    if (shading === "both") renderScene(side.scene, edgeMaterial);
    side.scene.remove(grid);
  };

  const frame = () => {
    const dt = clock.getDelta();
    for (const s of sides) s.mixer?.update(dt);
    controls.update();
    renderer.setScissorTest(true);
    renderer.setScissor(0, 0, width, height);
    renderer.clear();
    const [old, next] = sides;
    const split = mode === "side" && old.model && next.model;
    const panes: { side: Side; x: number; w: number }[] = split
      ? [
          { side: old, x: 0, w: Math.floor(width / 2) },
          { side: next, x: Math.floor(width / 2), w: width - Math.floor(width / 2) },
        ]
      : [{ side: next.model ? next : old, x: 0, w: width }];
    camera.aspect = panes[0].w / Math.max(height, 1);
    camera.updateProjectionMatrix();
    for (const p of panes) {
      renderer.setViewport(p.x, 0, p.w, height);
      renderer.setScissor(p.x, 0, p.w, height);
      renderShaded(p.side);
    }
    if (!split && mode === "overlay" && old.model && next.model) {
      renderer.clearDepth();
      renderScene(old.scene, ghostMaterial);
    }
  };
  renderer.setAnimationLoop(frame);

  let clipPlaying: string | null = null;
  const play = () => {
    for (const s of sides) {
      if (!s.mixer || !s.model) continue;
      s.mixer.stopAllAction();
      const clip = clipPlaying === null ? undefined : s.model.animations.find((a, i) => clipName(a, i) === clipPlaying);
      if (clip) s.mixer.clipAction(clip).reset().play();
    }
    clock.getDelta();
  };

  return {
    setModels(old, next) {
      [old, next].forEach((model, i) => {
        const side = sides[i];
        side.mixer?.stopAllAction();
        if (side.model && side.model !== model) {
          side.scene.remove(side.model.object);
          disposeModel(side.model);
        }
        side.model = model;
        side.mixer = model && model.animations.length ? new AnimationMixer(model.object) : null;
        if (model) side.scene.add(model.object);
      });
      play();
      fit();
    },
    setMode(m) {
      mode = m;
    },
    setShading(s) {
      shading = s;
    },
    playAnimation(name) {
      clipPlaying = name;
      play();
    },
    resetView: fit,
    resize(w, h) {
      width = Math.max(1, Math.floor(w));
      height = Math.max(1, Math.floor(h));
      renderer.setSize(width, height);
    },
    dispose() {
      renderer.setAnimationLoop(null);
      for (const s of sides) if (s.model) disposeModel(s.model);
      controls.dispose();
      renderer.dispose();
      renderer.domElement.remove();
    },
  };
}
