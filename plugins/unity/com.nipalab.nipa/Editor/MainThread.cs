using System;
using System.Collections.Concurrent;
using UnityEditor;

namespace Nipa.Editor
{
    [InitializeOnLoad]
    internal static class MainThread
    {
        static readonly ConcurrentQueue<Action> Queue = new ConcurrentQueue<Action>();

        static MainThread()
        {
            EditorApplication.update += Drain;
        }

        public static void Post(Action action)
        {
            Queue.Enqueue(action);
        }

        static void Drain()
        {
            while (Queue.TryDequeue(out var action))
            {
                try
                {
                    action();
                }
                catch (Exception e)
                {
                    UnityEngine.Debug.LogException(e);
                }
            }
        }
    }
}
