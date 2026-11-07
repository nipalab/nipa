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
    }
}
