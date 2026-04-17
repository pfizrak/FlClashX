import 'package:flclashx/clash/core.dart';
import 'package:flclashx/common/common.dart';
import 'package:flclashx/enum/enum.dart';
import 'package:flclashx/models/common.dart';
import 'package:flclashx/providers/config.dart';
import 'package:flclashx/providers/state.dart';
import 'package:flclashx/state.dart';
import 'package:flclashx/utils/device_info_service.dart';
import 'package:flclashx/widgets/widgets.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../providers/app.dart';

class DeveloperView extends ConsumerWidget {
  const DeveloperView({super.key});

  Future<void> _showUpdateDialogPreview(BuildContext context) async {
    final commonScaffoldState = context.commonScaffoldState;
    if (commonScaffoldState?.mounted != true) return;
    final data = await commonScaffoldState?.loadingRun<Map<String, dynamic>?>(
      request.fetchLatestReleaseForCurrentChannel,
      title: appLocalizations.checkUpdate,
    );
    await globalState.appController.checkUpdateResultHandle(
      data: data,
      handleError: true,
    );
  }

  Widget _getDeveloperList(BuildContext context, WidgetRef ref) => generateSectionV2(
      title: appLocalizations.options,
      items: [
        ListItem(
          title: Text(appLocalizations.messageTest),
          onTap: () {
            context.showNotifier(
              appLocalizations.messageTestTip,
            );
          },
        ),
        ListItem(
          title: Text(appLocalizations.logsTest),
          onTap: () {
            for (var i = 0; i < 1000; i++) {
              ref.read(requestsProvider.notifier).addRequest(Connection(
                    id: utils.id,
                    start: DateTime.now(),
                    metadata: Metadata(
                      uid: i * i,
                      network: utils.generateRandomString(
                        maxLength: 1000,
                        minLength: 20,
                      ),
                      sourceIP: '',
                      sourcePort: '',
                      destinationIP: '',
                      destinationPort: '',
                      host: '',
                      process: '',
                      remoteDestination: "",
                    ),
                    chains: ["chains"],
                  ));
              globalState.appController.addLog(
                Log.app(
                  utils.generateRandomString(
                    maxLength: 200,
                    minLength: 20,
                  ),
                ),
              );
            }
          },
        ),
        ListItem(
          title: Text(appLocalizations.crashTest),
          onTap: () {
            clashCore.clashInterface.crash();
          },
        ),
        ListItem(
          title: Text(appLocalizations.clearData),
          onTap: () async {
            await globalState.appController.handleClear();
          },
        ),
        ListItem(
          title: const Text("Show Update Dialog"),
          subtitle: const Text("Force open the current release notes popup"),
          onTap: () async {
            await _showUpdateDialogPreview(context);
          },
        )
      ],
    );

  Widget _getOverrideSection(BuildContext context, WidgetRef ref) {
    final appSetting = ref.watch(appSettingProvider);
    final currentHwid = appSetting.devOverrideHwid;
    final currentUa = appSetting.devOverrideUa;
    final configuredChannels = request.configuredUpdateChannels.toSet().toList();
    final currentUpdateChannel = request.currentUpdateChannel;
    final updateChannelSubtitle = configuredChannels.isEmpty
        ? currentUpdateChannel
        : "$currentUpdateChannel (${configuredChannels.join(", ")})";

    return generateSectionV2(
      title: "HWID / UA Override",
      items: [
        _DevCurrentHwidItem(),
        ListItem(
          leading: const Icon(Icons.system_update_alt),
          title: const Text("Update Channel"),
          subtitle: SelectableText(
            updateChannelSubtitle,
            style: Theme.of(context).textTheme.bodySmall,
          ),
        ),
        ListItem.input(
          leading: const Icon(Icons.fingerprint),
          title: const Text("Override HWID"),
          subtitle: Text(currentHwid ?? appLocalizations.defaultText),
          delegate: InputDelegate(
            title: "Override HWID",
            value: currentHwid ?? "",
            resetValue: "",
            onChanged: (value) {
              final newValue = (value == null || value.isEmpty) ? null : value;
              ref.read(appSettingProvider.notifier).updateState(
                    (state) => state.copyWith(
                      devOverrideHwid: newValue,
                    ),
                  );
            },
          ),
        ),
        ListItem.input(
          leading: const Icon(Icons.web),
          title: const Text("Override User-Agent"),
          subtitle: Text(currentUa ?? appLocalizations.defaultText),
          delegate: InputDelegate(
            title: "Override User-Agent",
            value: currentUa ?? "",
            resetValue: "",
            onChanged: (value) {
              final newValue = (value == null || value.isEmpty) ? null : value;
              ref.read(appSettingProvider.notifier).updateState(
                    (state) => state.copyWith(
                      devOverrideUa: newValue,
                    ),
                  );
            },
          ),
        ),
      ],
    );
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final manualEnable = ref.watch(
      appSettingProvider.select(
        (state) => state.developerMode,
      ),
    );
    final profileEnable = ref.watch(devModeEnabledProvider);
    final enable = manualEnable || profileEnable;
    return SingleChildScrollView(
      padding: baseInfoEdgeInsets,
      child: Column(
        children: [
          CommonCard(
            type: CommonCardType.filled,
            radius: 18,
            child: ListItem.switchItem(
              padding: const EdgeInsets.only(
                left: 16,
                right: 16,
              ),
              title: Text(appLocalizations.developerMode),
              delegate: SwitchDelegate(
                value: enable,
                onChanged: profileEnable ? null : (value) {
                  ref.read(appSettingProvider.notifier).updateState(
                        (state) => state.copyWith(
                          developerMode: value,
                        ),
                      );
                },
              ),
            ),
          ),
          const SizedBox(
            height: 16,
          ),
          _getOverrideSection(context, ref),
          const SizedBox(
            height: 16,
          ),
          _getDeveloperList(context, ref)
        ],
      ),
    );
  }
}

class _DevCurrentHwidItem extends StatelessWidget {
  @override
  Widget build(BuildContext context) {
    return FutureBuilder<String?>(
      future: DeviceInfoService().getDeviceDetails().then((d) => d.hwid),
      builder: (context, snapshot) {
        final realHwid = snapshot.data ?? "...";
        return ListItem(
          leading: const Icon(Icons.info_outline),
          title: const Text("Current HWID"),
          subtitle: SelectableText(
            realHwid,
            style: Theme.of(context).textTheme.bodySmall,
          ),
        );
      },
    );
  }
}
