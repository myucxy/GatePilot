import 'package:flutter/material.dart';

void main() {
  runApp(const GatePilotMobileApp());
}

/// 移动端 M0 壳应用。
/// 后续页面状态必须以 API 拉取结果为准，Push 和 WebSocket 只作为提醒入口。
class GatePilotMobileApp extends StatelessWidget {
  const GatePilotMobileApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'GatePilot',
      theme: ThemeData(
        colorScheme: ColorScheme.fromSeed(seedColor: const Color(0xFF0B5CAB)),
        useMaterial3: true,
      ),
      home: const ApprovalInboxPage(),
    );
  }
}

class ApprovalInboxPage extends StatefulWidget {
  const ApprovalInboxPage({super.key});

  @override
  State<ApprovalInboxPage> createState() => _ApprovalInboxPageState();
}

class _ApprovalInboxPageState extends State<ApprovalInboxPage> {
  bool _isChinese = true;

  Map<String, String> get _text => _isChinese
      ? {
          'title': 'GatePilot',
          'approvals': '审批',
          'approvalsSubtitle': '等待接入基于契约生成的 API',
          'devices': '设备',
          'devicesSubtitle': '支持 Android 9 及以上版本',
          'sessions': '会话',
          'sessionsSubtitle': '实时提醒通过 WebSocket，最终状态通过 API 刷新',
        }
      : {
          'title': 'GatePilot',
          'approvals': 'Approvals',
          'approvalsSubtitle': 'Waiting for schema-backed API integration',
          'devices': 'Devices',
          'devicesSubtitle': 'Android 9+ supported',
          'sessions': 'Sessions',
          'sessionsSubtitle': 'Real-time updates will use WebSocket + API refresh',
        };

  @override
  Widget build(BuildContext context) {
    final text = _text;

    return Scaffold(
      appBar: AppBar(
        title: Text(text['title']!),
        actions: [
          // 移动端默认中文，当前切换只影响页面状态；后续可接入系统语言和本地持久化。
          SegmentedButton<bool>(
            segments: const [
              ButtonSegment(value: true, label: Text('中文')),
              ButtonSegment(value: false, label: Text('EN')),
            ],
            selected: {_isChinese},
            onSelectionChanged: (value) => setState(() => _isChinese = value.first),
          ),
          const SizedBox(width: 8),
        ],
      ),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          _StatusTile(title: text['approvals']!, subtitle: text['approvalsSubtitle']!),
          _StatusTile(title: text['devices']!, subtitle: text['devicesSubtitle']!),
          _StatusTile(title: text['sessions']!, subtitle: text['sessionsSubtitle']!),
        ],
      ),
    );
  }
}

class _StatusTile extends StatelessWidget {
  const _StatusTile({required this.title, required this.subtitle});

  final String title;
  final String subtitle;

  @override
  Widget build(BuildContext context) {
    return Card(
      child: ListTile(
        title: Text(title),
        subtitle: Text(subtitle),
      ),
    );
  }
}
