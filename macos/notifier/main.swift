import AppKit
import UserNotifications

enum Exit: Int32 {
    case submitted = 0
    case usage = 2
    case pending = 3
    case denied = 4
    case failed = 5
}

let patience: TimeInterval = 5

let chrome = "com.google.Chrome"

let texts = ["first": "Primo round pronto", "update": "Round aggiornato"]

func page(_ raw: String) -> URL? {
    guard let url = URL(string: raw), url.scheme == "http", url.host == "127.0.0.1",
          url.port != nil, url.path.hasPrefix("/s/"), url.query == nil, url.fragment == nil
    else { return nil }
    return url
}

func code(_ s: String) -> FourCharCode { s.utf8.reduce(0) { $0 << 8 | FourCharCode($1) } }

let focusTab = """
on findtab(page)
	tell application id "com.google.Chrome"
		repeat with w in windows
			set i to 0
			repeat with t in tabs of w
				set i to i + 1
				if (URL of t) starts with page then return {w, i}
			end repeat
		end repeat
	end tell
	return missing value
end findtab

on focustab(page)
	set found to findtab(page)
	if found is missing value then return false
	set {w, i} to found
	tell application id "com.google.Chrome"
		if minimized of w then set minimized of w to false
		set active tab index of w to i
		set index of w to 1
		activate
	end tell
	return true
end focustab
"""

func focusChrome(_ url: URL) -> Bool {
    guard !NSRunningApplication.runningApplications(withBundleIdentifier: chrome).isEmpty,
          let script = NSAppleScript(source: focusTab) else { return false }
    let call = NSAppleEventDescriptor(eventClass: code("ascr"), eventID: code("psbr"),
                                      targetDescriptor: .currentProcess(), returnID: AEReturnID(kAutoGenerateReturnID),
                                      transactionID: AETransactionID(kAnyTransactionID))
    call.setParam(NSAppleEventDescriptor(string: "focustab"), forKeyword: code("snam"))
    let args = NSAppleEventDescriptor.list()
    args.insert(NSAppleEventDescriptor(string: url.absoluteString), at: 1)
    call.setParam(args, forKeyword: code("----"))
    var failure: NSDictionary?
    let done = script.executeAppleEvent(call, error: &failure)
    if let failure {
        NSLog("lavagna-notifier: focustab failed: %@", String(describing: failure[NSAppleScript.errorNumber] ?? "?"))
        return false
    }
    return done.booleanValue
}

func focusRex(_ url: URL, _ target: String) -> Bool {
    guard !target.isEmpty, target.utf8.count <= 16 * 1024,
          let resource = Bundle.main.url(forResource: "lavagna-path", withExtension: nil),
          let raw = try? String(contentsOf: resource, encoding: .utf8) else { return false }
    let binary = raw.trimmingCharacters(in: .newlines)
    guard binary.hasPrefix("/"), FileManager.default.isExecutableFile(atPath: binary) else { return false }
    let task = Process()
    task.executableURL = URL(fileURLWithPath: binary)
    task.arguments = ["rex-click", url.absoluteString, target]
    task.standardInput = FileHandle.nullDevice
    task.standardOutput = FileHandle.nullDevice
    task.standardError = FileHandle.nullDevice
    do { try task.run() } catch { return false }
    let timeout = DispatchWorkItem { if task.isRunning { task.terminate() } }
    DispatchQueue.global().asyncAfter(deadline: .now() + 15, execute: timeout)
    task.waitUntilExit()
    timeout.cancel()
    return task.terminationReason == .exit && task.terminationStatus == 0
}

func activateRex() -> Bool {
    guard let app = NSRunningApplication.runningApplications(withBundleIdentifier: "com.superlogical.rex").first
    else { return false }
    return app.activate(options: [.activateAllWindows])
}

final class Notifier: NSObject, NSApplicationDelegate, UNUserNotificationCenterDelegate {
    let center = UNUserNotificationCenter.current()
    var handlingClick = false

    func applicationWillFinishLaunching(_ note: Notification) {
        center.delegate = self
    }

    func applicationDidFinishLaunching(_ note: Notification) {
        let args = Array(CommandLine.arguments.dropFirst())
        switch args.count {
        case 0:
            DispatchQueue.main.asyncAfter(deadline: .now() + 10) {
                if !self.handlingClick { exit(0) }
            }
        case 1 where args[0] == "authorize":
            authorize { _ in exit(Exit.submitted.rawValue) }
        case 2:
            post(args[0], args[1])
        case 4 where args[2] == "--rex" && !args[3].isEmpty && args[3].utf8.count <= 16 * 1024:
            post(args[0], args[1], rex: args[3])
        default:
            exit(Exit.usage.rawValue)
        }
    }

    func authorize(_ then: @escaping (Bool) -> Void) {
        DispatchQueue.main.asyncAfter(deadline: .now() + patience) { exit(Exit.pending.rawValue) }
        center.requestAuthorization(options: [.alert, .sound]) { granted, _ in
            DispatchQueue.main.async { then(granted) }
        }
    }

    func post(_ kind: String, _ raw: String, rex: String? = nil) {
        guard let text = texts[kind], let url = page(raw) else { exit(Exit.usage.rawValue) }
        authorize { granted in
            guard granted else { exit(Exit.denied.rawValue) }
            let content = UNMutableNotificationContent()
            content.title = "Lavagna"
            content.body = text
            content.sound = .default
            content.userInfo = ["page": url.absoluteString]
            if let rex { content.userInfo["rex"] = rex }
            let request = UNNotificationRequest(identifier: UUID().uuidString, content: content, trigger: nil)
            self.center.add(request) { error in
                exit(error == nil ? Exit.submitted.rawValue : Exit.failed.rawValue)
            }
        }
    }

    func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse,
                                withCompletionHandler done: @escaping () -> Void) {
        handlingClick = true
        if response.actionIdentifier == UNNotificationDefaultActionIdentifier,
           let url = page(response.notification.request.content.userInfo["page"] as? String ?? "") {
            if let target = response.notification.request.content.userInfo["rex"] as? String {
                DispatchQueue.global(qos: .userInitiated).async {
                    let focused = focusRex(url, target)
                    DispatchQueue.main.async {
                        if !focused || !activateRex() {
                            NSLog("lavagna-notifier: cannot show the Rex panel")
                            let alert = NSAlert()
                            alert.messageText = "Impossibile aprire Lavagna in Rex"
                            alert.informativeText = "Verifica che Rex e la sessione siano aperti e che terminal-browser sia installato. Abilita Remote Control in Rex Settings → Rex Server. Se la sessione è cambiata, riprendi il round per aggiornare la destinazione della notifica."
                            alert.addButton(withTitle: "OK")
                            alert.runModal()
                        }
                        done()
                        DispatchQueue.main.asyncAfter(deadline: .now() + 1) { exit(0) }
                    }
                }
                return
            }
            if !focusChrome(url) { NSWorkspace.shared.open(url) }
        }
        done()
        DispatchQueue.main.asyncAfter(deadline: .now() + 1) { exit(0) }
    }

    func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification,
                                withCompletionHandler done: @escaping (UNNotificationPresentationOptions) -> Void) {
        done([.banner, .list, .sound])
    }
}

let app = NSApplication.shared
let notifier = Notifier()
app.delegate = notifier
app.run()
