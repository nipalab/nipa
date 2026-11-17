using System;

namespace Nipa.Editor.Core.Daemon
{
    public sealed class DaemonException : Exception
    {
        public int Code { get; }
        public MaintenanceWindow Maintenance { get; }

        public DaemonException(int code, string message, MaintenanceWindow maintenance = null) : base(message)
        {
            Code = code;
            Maintenance = maintenance;
        }
    }
}
