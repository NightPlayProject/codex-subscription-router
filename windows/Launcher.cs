using System;
using System.IO;
using System.Diagnostics;
using System.Windows.Forms;

class CombinedLauncher {
    [STAThread] static void Main() {
        try {
            var dir = AppDomain.CurrentDomain.BaseDirectory;
            var start = new ProcessStartInfo(
                Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.System), "WindowsPowerShell\\v1.0\\powershell.exe"),
                "-NoProfile -ExecutionPolicy Bypass -File \"" + Path.Combine(dir, "Start.ps1") + "\"");
            start.UseShellExecute = false;
            start.CreateNoWindow = true;
            start.WindowStyle = ProcessWindowStyle.Hidden;
            start.WorkingDirectory = dir;
            using (var child = Process.Start(start)) { child.WaitForExit(); }
        } catch (Exception e) { MessageBox.Show(e.Message, "Codex Subscription Router"); }
    }
}
