import UIKit
import Zeuscore

@main
final class AppDelegate: UIResponder, UIApplicationDelegate {
    var window: UIWindow?

    func application(_ application: UIApplication, didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]?) -> Bool {
        // The Go engine handles sign-in, settings and servers in the app;
        // the connection itself runs in the PacketTunnel extension.
        ZeuscoreStart(Shared.dataDir.path, "ios", CoreHost.shared, TunnelController.shared)
        TunnelController.shared.load {
            if TunnelController.shared.isIdle {
                ZeuscoreAutoConnect(false)
            }
        }

        let window = UIWindow(frame: UIScreen.main.bounds)
        window.rootViewController = WebViewController()
        window.makeKeyAndVisible()
        self.window = window
        return true
    }

    func applicationDidBecomeActive(_ application: UIApplication) {
        TunnelController.shared.refresh()
    }
}
