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
        }

        [Test]
        public void WindowOpens()
        {
            var w = EditorWindow.GetWindow<NipaWindow>();
            Assert.That(w, Is.Not.Null);
            w.Close();
        }

        static object Call(string method, params object[] args)
        {
            var m = typeof(NipaAssetProcessor).GetMethod(method, System.Reflection.BindingFlags.NonPublic | System.Reflection.BindingFlags.Static);
            Assert.That(m, Is.Not.Null, method);
            return m.Invoke(null, args);
        }

        [Test]
        public void AssetProcessorAllowsEverythingWithoutSession()
        {
            var args = new object[] { "Assets/a.prefab", null };
            Assert.That((bool)Call("IsOpenForEdit", args), Is.True);
            Assert.That((bool)Call("MakeEditable", new object[] { new[] { "Assets/a.prefab" } }), Is.True);
            var paths = new[] { "Assets/a.prefab" };
            Assert.That((string[])Call("OnWillSaveAssets", new object[] { paths }), Is.EqualTo(paths));
            Assert.That(Call("OnWillMoveAsset", "Assets/a", "Assets/b"), Is.EqualTo(AssetMoveResult.DidNotMove));
            Assert.That(Call("OnWillDeleteAsset", "Assets/a", RemoveAssetOptions.MoveAssetToTrash), Is.EqualTo(AssetDeleteResult.DidNotDelete));
        }
    }
}
