using UnityEditor;
using UnityEditor.Overlays;
using UnityEditor.Toolbars;
using UnityEngine.UIElements;

namespace Nipa.Editor
{
    [Overlay(typeof(SceneView), "Nipa")]
    internal sealed class NipaSceneOverlay : ToolbarOverlay
    {
        public NipaSceneOverlay() : base(NipaBranchDropdown.Id)
        {
        }
    }

    [EditorToolbarElement(Id, typeof(SceneView))]
    internal sealed class NipaBranchDropdown : EditorToolbarDropdown
    {
        public const string Id = "Nipa/BranchDropdown";

        public NipaBranchDropdown()
        {
            tooltip = "Switch Nipa branch or tag";
            clicked += NipaBranches.ShowMenu;
            Refresh();
            NipaHost.Changed += Refresh;
            RegisterCallback<DetachFromPanelEvent>(_ => NipaHost.Changed -= Refresh);
        }

        void Refresh()
        {
            text = NipaBranches.Label();
        }
    }
}
