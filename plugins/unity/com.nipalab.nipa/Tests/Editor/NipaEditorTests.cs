using System.Linq;
using NUnit.Framework;
using UnityEditor;
using UnityEditor.VersionControl;
using UnityEngine;

namespace Nipa.Editor.Tests
{
    public class NipaEditorTests
    {
        [Test]
        public void ProviderIsRegistered()
        {
            var attrs = typeof(NipaVersionControl).GetCustomAttributes(typeof(VersionControlAttribute), false);
            Assert.That(attrs, Is.Not.Empty);
            Assert.That(typeof(VersionControlObject).IsAssignableFrom(typeof(NipaVersionControl)));
        }

        [Test]
        public void NotConnectedWithoutDaemon()
        {
            var vc = ScriptableObject.CreateInstance<NipaVersionControl>();
            try
            {
                Assert.That(vc.isConnected, Is.False);
                Assert.DoesNotThrow(vc.Refresh);
            }
            finally
            {
                Object.DestroyImmediate(vc);
            }
        }

        [Test]
        public void ExposesExtensions()
        {
            var vc = ScriptableObject.CreateInstance<NipaVersionControl>();
            try
            {
                Assert.That(vc.GetExtension<IIconOverlayExtension>(), Is.Not.Null);
                Assert.That(vc.GetExtension<IPopupMenuExtension>(), Is.Not.Null);
                Assert.That(vc.GetExtension<ISettingsInspectorExtension>(), Is.Not.Null);
            }
            finally
            {
                Object.DestroyImmediate(vc);
            }
        }

        [Test]
        public void MenusAreDisabledWithoutSession()
        {
            Assert.That(Menu.GetEnabled("Assets/Nipa/Stage"), Is.False);
            Assert.That(Menu.GetEnabled("Assets/Nipa/Restore"), Is.False);
            Assert.That(Menu.GetEnabled("Assets/Nipa/Refresh"), Is.False);
            Assert.That(Menu.GetEnabled("Assets/Nipa/Ask to Release..."), Is.False);
            Assert.That(Menu.GetEnabled("Assets/Nipa/Notify When Free"), Is.False);
        }

        [Test]
        public void WindowOpens()
        {
            var w = EditorWindow.GetWindow<NipaWindow>();
            Assert.That(w, Is.Not.Null);
            w.Close();
        }

        [Test]
        public void WindowOpensOnShelvesTab()
        {
            NipaWindow.OpenTab(NipaWindow.ShelvesTab);
            var w = EditorWindow.GetWindow<NipaWindow>();
            Assert.That(w, Is.Not.Null);
            w.Close();
        }

        static System.Type EditorType(string name) => typeof(NipaWindow).Assembly.GetType("Nipa.Editor." + name);

        [Test]
        public void CompareWindowOpensWithoutSession()
        {
            var open = EditorType("NipaImageCompare").GetMethod("Open", System.Reflection.BindingFlags.Public | System.Reflection.BindingFlags.Static);
            open.Invoke(null, new object[] { "Assets/a.png", null, "Mine", new Nipa.Editor.Core.Daemon.BlobRef { Source = "working", Path = "Assets/a.png" }, "Theirs" });
            var w = Resources.FindObjectsOfTypeAll(EditorType("NipaImageCompare")).OfType<EditorWindow>().FirstOrDefault();
            Assert.That(w, Is.Not.Null);
            w.Close();
        }

        [Test]
        public void MaintenanceGateAllowsWithoutSession()
        {
            var host = EditorType("NipaHost");
            Assert.That(host.GetProperty("Maintenance").GetValue(null), Is.Null);
            Assert.That((bool)host.GetMethod("Allowed").Invoke(null, new object[] { "Push", false }), Is.True);
        }

        static object Call(string method, params object[] args)
        {
            var m = typeof(NipaAssetProcessor).GetMethods(System.Reflection.BindingFlags.NonPublic | System.Reflection.BindingFlags.Static)
                .FirstOrDefault(x => x.Name == method && x.GetParameters().Length == args.Length);
            Assert.That(m, Is.Not.Null, method);
            return m.Invoke(null, args);
        }

        [Test]
        public void AssetProcessorAllowsEverythingWithoutSession()
        {
            var args = new object[] { "Assets/a.prefab", null };
            Assert.That((bool)Call("IsOpenForEdit", args), Is.True);
            Assert.That((bool)Call("MakeEditable", new object[] { new[] { "Assets/a.prefab" } }), Is.True);
            var notEditable = new System.Collections.Generic.List<string>();
            Assert.That((bool)Call("MakeEditable", new object[] { new[] { "Assets/a.prefab" }, "", notEditable }), Is.True);
            Assert.That((bool)Call("CanOpenForEdit", new object[] { new[] { "Assets/a.prefab" }, notEditable, StatusQueryOptions.UseCachedIfPossible }), Is.True);
            Assert.That((bool)Call("IsOpenForEdit", new object[] { "Assets/a.prefab", null, StatusQueryOptions.UseCachedIfPossible }), Is.True);
            Assert.That(notEditable, Is.Empty);
            var paths = new[] { "Assets/a.prefab" };
            Assert.That((string[])Call("OnWillSaveAssets", new object[] { paths }), Is.EqualTo(paths));
            Assert.That(Call("OnWillMoveAsset", "Assets/a", "Assets/b"), Is.EqualTo(AssetMoveResult.DidNotMove));
            Assert.That(Call("OnWillDeleteAsset", "Assets/a", RemoveAssetOptions.MoveAssetToTrash), Is.EqualTo(AssetDeleteResult.DidNotDelete));
        }
    }
}
