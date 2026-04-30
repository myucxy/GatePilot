import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';

void main() {
  runApp(const GatePilotMobileApp());
}

const _tenantId = '00000000-0000-0000-0000-000000000100';

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
  final _serverController = TextEditingController(text: 'http://10.0.2.2:8080');
  bool _isChinese = true;
  bool _loading = false;
  int _tab = 0;
  String _error = '';
  List<Map<String, dynamic>> _approvals = [];
  List<Map<String, dynamic>> _devices = [];
  List<Map<String, dynamic>> _sessions = [];

  Map<String, String> get _text => _isChinese
      ? {
          'title': 'GatePilot',
          'server': '服务地址',
          'refresh': '刷新',
          'approvals': '审批',
          'devices': '设备',
          'sessions': '会话',
          'emptyApprovals': '暂无审批',
          'emptyDevices': '暂无设备',
          'emptySessions': '暂无会话',
          'approve': '批准',
          'reject': '拒绝',
          'loading': '加载中',
          'error': '请求失败',
        }
      : {
          'title': 'GatePilot',
          'server': 'Server URL',
          'refresh': 'Refresh',
          'approvals': 'Approvals',
          'devices': 'Devices',
          'sessions': 'Sessions',
          'emptyApprovals': 'No approvals',
          'emptyDevices': 'No devices',
          'emptySessions': 'No sessions',
          'approve': 'Approve',
          'reject': 'Reject',
          'loading': 'Loading',
          'error': 'Request failed',
        };

  @override
  void dispose() {
    _serverController.dispose();
    super.dispose();
  }

  Future<void> _refresh() async {
    setState(() {
      _loading = true;
      _error = '';
    });
    try {
      final approvals = await _getList('/api/v1/tenants/$_tenantId/approvals');
      final devices = await _getList('/api/v1/tenants/$_tenantId/devices');
      var sessions = <Map<String, dynamic>>[];
      if (devices.isNotEmpty) {
        sessions = await _getList('/api/v1/devices/${devices.first['device_id']}/sessions');
      }
      if (!mounted) return;
      setState(() {
        _approvals = approvals;
        _devices = devices;
        _sessions = sessions;
      });
    } catch (error) {
      if (!mounted) return;
      setState(() => _error = '${_text['error']}: $error');
    } finally {
      if (mounted) {
        setState(() => _loading = false);
      }
    }
  }

  Future<void> _decide(String approvalId, String decisionType) async {
    setState(() {
      _loading = true;
      _error = '';
    });
    try {
      await _post('/api/v1/approvals/$approvalId/decision', {
        'decision_type': decisionType,
        'payload': '',
      });
      await _refresh();
    } catch (error) {
      if (!mounted) return;
      setState(() => _error = '${_text['error']}: $error');
    } finally {
      if (mounted) {
        setState(() => _loading = false);
      }
    }
  }

  Future<List<Map<String, dynamic>>> _getList(String path) async {
    final body = await _request('GET', path);
    final rawItems = ((body['data'] as Map<String, dynamic>)['items'] as List<dynamic>? ?? []);
    return rawItems.cast<Map<String, dynamic>>();
  }

  Future<void> _post(String path, Map<String, dynamic> payload) async {
    await _request('POST', path, payload: payload);
  }

  Future<Map<String, dynamic>> _request(String method, String path, {Map<String, dynamic>? payload}) async {
    final base = Uri.parse(_serverController.text.trim().replaceAll(RegExp(r'/$'), ''));
    final uri = base.resolve(path);
    final client = HttpClient();
    try {
      final request = await client.openUrl(method, uri);
      request.headers.contentType = ContentType.json;
      request.headers.add('Idempotency-Key', DateTime.now().microsecondsSinceEpoch.toString());
      if (payload != null) {
        request.write(jsonEncode(payload));
      }
      final response = await request.close();
      final text = await response.transform(utf8.decoder).join();
      if (response.statusCode >= 300) {
        throw '${response.statusCode} $text';
      }
      return jsonDecode(text) as Map<String, dynamic>;
    } finally {
      client.close(force: true);
    }
  }

  @override
  Widget build(BuildContext context) {
    final text = _text;

    return Scaffold(
      appBar: AppBar(
        title: Text(text['title']!),
        actions: [
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
      body: Column(
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 8, 16, 12),
            child: Row(
              children: [
                Expanded(
                  child: TextField(
                    controller: _serverController,
                    decoration: InputDecoration(
                      labelText: text['server'],
                      border: const OutlineInputBorder(),
                    ),
                  ),
                ),
                const SizedBox(width: 8),
                FilledButton.icon(
                  onPressed: _loading ? null : _refresh,
                  icon: const Icon(Icons.refresh),
                  label: Text(text['refresh']!),
                ),
              ],
            ),
          ),
          if (_loading) LinearProgressIndicator(semanticsLabel: text['loading']),
          if (_error.isNotEmpty)
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
              child: Text(_error, style: TextStyle(color: Theme.of(context).colorScheme.error)),
            ),
          Expanded(child: _currentBody(text)),
        ],
      ),
      bottomNavigationBar: NavigationBar(
        selectedIndex: _tab,
        onDestinationSelected: (value) => setState(() => _tab = value),
        destinations: [
          NavigationDestination(icon: const Icon(Icons.fact_check), label: text['approvals']!),
          NavigationDestination(icon: const Icon(Icons.devices), label: text['devices']!),
          NavigationDestination(icon: const Icon(Icons.terminal), label: text['sessions']!),
        ],
      ),
    );
  }

  Widget _currentBody(Map<String, String> text) {
    switch (_tab) {
      case 1:
        return _DeviceList(items: _devices, emptyText: text['emptyDevices']!);
      case 2:
        return _SessionList(items: _sessions, emptyText: text['emptySessions']!);
      default:
        return _ApprovalList(
          items: _approvals,
          emptyText: text['emptyApprovals']!,
          approveText: text['approve']!,
          rejectText: text['reject']!,
          onDecide: _decide,
        );
    }
  }
}

class _ApprovalList extends StatelessWidget {
  const _ApprovalList({
    required this.items,
    required this.emptyText,
    required this.approveText,
    required this.rejectText,
    required this.onDecide,
  });

  final List<Map<String, dynamic>> items;
  final String emptyText;
  final String approveText;
  final String rejectText;
  final Future<void> Function(String approvalId, String decisionType) onDecide;

  @override
  Widget build(BuildContext context) {
    if (items.isEmpty) {
      return _EmptyState(text: emptyText);
    }
    return ListView.separated(
      padding: const EdgeInsets.all(16),
      itemCount: items.length,
      separatorBuilder: (_, _) => const SizedBox(height: 8),
      itemBuilder: (context, index) {
        final item = items[index];
        final status = item['status']?.toString() ?? '';
        return Card(
          child: Padding(
            padding: const EdgeInsets.all(12),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(item['prompt_text']?.toString() ?? item['event_type']?.toString() ?? ''),
                const SizedBox(height: 4),
                Text('${item['cli_type']} / ${item['risk_level']} / $status'),
                if (status == 'waiting_decision') ...[
                  const SizedBox(height: 8),
                  Wrap(
                    spacing: 8,
                    children: [
                      FilledButton(
                        onPressed: () => onDecide(item['approval_id'].toString(), 'approve'),
                        child: Text(approveText),
                      ),
                      OutlinedButton(
                        onPressed: () => onDecide(item['approval_id'].toString(), 'reject'),
                        child: Text(rejectText),
                      ),
                    ],
                  ),
                ],
              ],
            ),
          ),
        );
      },
    );
  }
}

class _DeviceList extends StatelessWidget {
  const _DeviceList({required this.items, required this.emptyText});

  final List<Map<String, dynamic>> items;
  final String emptyText;

  @override
  Widget build(BuildContext context) {
    if (items.isEmpty) {
      return _EmptyState(text: emptyText);
    }
    return ListView(
      padding: const EdgeInsets.all(16),
      children: items
          .map(
            (item) => Card(
              child: ListTile(
                title: Text(item['name']?.toString() ?? ''),
                subtitle: Text('${item['platform']} / ${item['arch']}'),
                trailing: Text(item['status']?.toString() ?? ''),
              ),
            ),
          )
          .toList(),
    );
  }
}

class _SessionList extends StatelessWidget {
  const _SessionList({required this.items, required this.emptyText});

  final List<Map<String, dynamic>> items;
  final String emptyText;

  @override
  Widget build(BuildContext context) {
    if (items.isEmpty) {
      return _EmptyState(text: emptyText);
    }
    return ListView(
      padding: const EdgeInsets.all(16),
      children: items
          .map(
            (item) => Card(
              child: ListTile(
                title: Text(item['cli_type']?.toString() ?? ''),
                subtitle: Text(item['last_output_summary']?.toString() ?? ''),
                trailing: Text(item['status']?.toString() ?? ''),
              ),
            ),
          )
          .toList(),
    );
  }
}

class _EmptyState extends StatelessWidget {
  const _EmptyState({required this.text});

  final String text;

  @override
  Widget build(BuildContext context) {
    return Center(child: Text(text));
  }
}
