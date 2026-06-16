{#
# Cloudflared Settings View
# Copyright (C) 2025-2026
#}

<script>
    function selectedCloudflaredOptionValue(optionMap) {
        if (!optionMap) {
            return '';
        }

        for (const [key, value] of Object.entries(optionMap)) {
            if (value && value.selected === 1) {
                return key;
            }
        }
        return '';
    }

    function ensureCloudflaredSelectOptions(selector, options) {
        var select = $(selector);
        if (!select.length || select.find('option').length > 0) {
            return;
        }

        options.forEach(function (option) {
            select.append($('<option>', {
                value: option.value,
                text: option.label
            }));
        });

        if (select.data('selectpicker')) {
            select.selectpicker('refresh');
        }
    }

    $(document).ready(function () {
        function optionPairs(optionMap) {
            if (!optionMap) {
                return [];
            }

            return Object.entries(optionMap)
                .filter(([key]) => key !== '')
                .map(([key, value]) => ({
                    value: key,
                    label: value.value
                }));
        }

        function applyModelState(data) {
            var general = data && data.settings ? data.settings.general : null;
            if (!general) {
                return;
            }

            ensureCloudflaredSelectOptions('#general\\.mode', optionPairs(general.mode));
            ensureCloudflaredSelectOptions('#general\\.edge_ip_version', optionPairs(general.edge_ip_version));
            ensureCloudflaredSelectOptions('#general\\.protocol', optionPairs(general.protocol));
            ensureCloudflaredSelectOptions('#general\\.loglevel', optionPairs(general.loglevel));

            $('#general\\.enabled').prop('checked', general.enabled === '1');
            $('#general\\.token').val(general.token || '');
            $('#general\\.tunnel_name').val(general.tunnel_name || '');
            $('#general\\.post_quantum').prop('checked', general.post_quantum === '1');

            var modeValue = selectedCloudflaredOptionValue(general.mode);
            var edgeValue = selectedCloudflaredOptionValue(general.edge_ip_version);
            var protocolValue = selectedCloudflaredOptionValue(general.protocol);
            var logLevelValue = selectedCloudflaredOptionValue(general.loglevel);

            if ($('#general\\.mode').data('selectpicker')) {
                $('#general\\.mode').selectpicker('val', modeValue);
                $('#general\\.edge_ip_version').selectpicker('val', edgeValue);
                $('#general\\.protocol').selectpicker('val', protocolValue);
                $('#general\\.loglevel').selectpicker('val', logLevelValue);
                $('.selectpicker').selectpicker('refresh');
            } else {
                $('#general\\.mode').val(modeValue);
                $('#general\\.edge_ip_version').val(edgeValue);
                $('#general\\.protocol').val(protocolValue);
                $('#general\\.loglevel').val(logLevelValue);
            }
        }

        function renderIngressRows(data) {
            var tunnelEntries = data && data.settings && data.settings.tunnels ? data.settings.tunnels.tunnel : null;
            var tbody = $('#tunnelRulesBody');
            tbody.empty();

            if (!tunnelEntries || Object.keys(tunnelEntries).length === 0) {
                tbody.append(
                    $('<tr>').append(
                        $('<td>', {
                            colspan: 5,
                            class: 'text-muted'
                        }).text('{{ lang._('No ingress rules are configured yet.') }}')
                    )
                );
                return;
            }

            Object.entries(tunnelEntries).forEach(function ([uuid, tunnel]) {
                var enabledText = (tunnel.enabled || '0') === '1' ? '{{ lang._('Yes') }}' : '{{ lang._('No') }}';
                var serviceValue = typeof tunnel.service === 'string' ? tunnel.service : selectedCloudflaredOptionValue(tunnel.service);
                var row = $('<tr>', { 'data-uuid': uuid });
                row.append($('<td>').text(enabledText));
                row.append($('<td>').text(tunnel.hostname || ''));
                row.append($('<td>').text(serviceValue || ''));
                row.append($('<td>').append($('<code>').text(tunnel.url || '')));

                var actionsCell = $('<td>');
                actionsCell.append(
                    $('<button>', {
                        type: 'button',
                        class: 'btn btn-xs btn-default'
                    }).append($('<i>', { class: 'fa fa-pencil' })).append(' {{ lang._('Edit') }}').on('click', function () {
                        editTunnel(uuid);
                    })
                );
                actionsCell.append(' ');
                actionsCell.append(
                    $('<button>', {
                        type: 'button',
                        class: 'btn btn-xs btn-danger'
                    }).append($('<i>', { class: 'fa fa-trash' })).append(' {{ lang._('Delete') }}').on('click', function () {
                        deleteTunnel(uuid);
                    })
                );
                row.append(actionsCell);
                tbody.append(row);
            });
        }

        function updateModeUI() {
            var mode = $('#general\\.mode').val();
            var isTokenMode = mode !== 'config';

            $('#row_general\\.token').toggle(isTokenMode);
            $('#ingressModeHint').toggle(isTokenMode);
            $('#ingressRulesPanel').toggle(!isTokenMode);
            $('#ingress_tab_nav').toggle(!isTokenMode);

            if (isTokenMode && window.location.hash === '#ingress') {
                $('a[href="#settings"]').tab('show');
            }
        }

        function activateRequestedTab() {
            var selectedTab = window.location.hash !== '' ? window.location.hash : '#settings';
            var selectedTabLink = $('a[href="' + selectedTab + '"]');
            if (selectedTabLink.length > 0 && selectedTabLink.is(':visible')) {
                selectedTabLink.tab('show');
            } else {
                $('a[href="#settings"]').tab('show');
            }
        }

        $.getJSON('/api/cloudflared/settings/get', function (data) {
            applyModelState(data);
            renderIngressRows(data);
            formatTokenizersUI();
            $('.selectpicker').selectpicker('refresh');
            updateModeUI();
            activateRequestedTab();
        });

        $('#general\\.mode').change(function () {
            updateModeUI();
        });

        $('a[data-toggle="tab"]').on('shown.bs.tab', function (e) {
            history.replaceState(null, null, e.target.hash);
        });

        $(window).on('hashchange', function () {
            activateRequestedTab();
        });

        $("#reconfigureAct").SimpleActionButton({
            onPreAction: function () {
                const dfObj = new $.Deferred();
                saveFormToEndpoint("/api/cloudflared/settings/set", 'frm_settings', function () {
                    dfObj.resolve();
                }, true, function () {
                    dfObj.reject();
                });
                return dfObj;
            }
        });
    });

    function addTunnel() {
        ensureCloudflaredSelectOptions('#tunnel\\.service', [
            { value: 'http', label: 'http' },
            { value: 'https', label: 'https' },
            { value: 'tcp', label: 'tcp' },
            { value: 'ssh', label: 'ssh' },
            { value: 'rdp', label: 'rdp' }
        ]);
        $('#DialogTunnel .modal-title').text('{{ lang._('Add Ingress Rule') }}');
        $('#frm_tunnel')[0].reset();
        $('#DialogTunnel').data('uuid', '');
        $('#DialogTunnel').modal('show');
    }

    function editTunnel(uuid) {
        $('#DialogTunnel .modal-title').text('{{ lang._('Edit Ingress Rule') }}');
        ensureCloudflaredSelectOptions('#tunnel\\.service', [
            { value: 'http', label: 'http' },
            { value: 'https', label: 'https' },
            { value: 'tcp', label: 'tcp' },
            { value: 'ssh', label: 'ssh' },
            { value: 'rdp', label: 'rdp' }
        ]);
        $.ajax({
            url: '/api/cloudflared/settings/getTunnel/' + uuid,
            type: 'GET',
            success: function (data) {
                if (data.tunnel) {
                    var tunnelServiceValue = typeof data.tunnel.service === 'string'
                        ? data.tunnel.service
                        : selectedCloudflaredOptionValue(data.tunnel.service);
                    $('#tunnel\\.enabled').prop('checked', (data.tunnel.enabled || '1') === '1');
                    $('#tunnel\\.hostname').val(data.tunnel.hostname || '');
                    $('#tunnel\\.service').val(tunnelServiceValue || 'http');
                    $('#tunnel\\.url').val(data.tunnel.url || '');
                    if ($('#tunnel\\.service').data('selectpicker')) {
                        $('#tunnel\\.service').selectpicker('val', tunnelServiceValue || 'http');
                        $('#tunnel\\.service').selectpicker('refresh');
                    }
                    $('#DialogTunnel').data('uuid', uuid);
                    $('#DialogTunnel').modal('show');
                }
            }
        });
    }

    function deleteTunnel(uuid) {
        stdDialogConfirm(
            '{{ lang._('Confirm Delete') }}',
            '{{ lang._('Delete this ingress rule?') }}',
            '{{ lang._('Yes') }}',
            '{{ lang._('No') }}',
            function () {
                $.ajax({
                    url: '/api/cloudflared/settings/delTunnel/' + uuid,
                    type: 'POST',
                    success: function () {
                        location.reload();
                    }
                });
            }
        );
    }

    function saveTunnel() {
        var uuid = $('#DialogTunnel').data('uuid');
        var url = uuid ? '/api/cloudflared/settings/setTunnel/' + uuid : '/api/cloudflared/settings/addTunnel';
        saveFormToEndpoint(url, 'frm_tunnel', function () {
            $('#DialogTunnel').modal('hide');
            location.reload();
        }, true, function () {
            // keep inline validation inside the modal without stacking another warning dialog
        });
    }
</script>

<ul class="nav nav-tabs" data-tabs="tabs" id="maintabs">
    <li class="active"><a data-toggle="tab" href="#settings">{{ lang._('Settings') }}</a></li>
    <li id="ingress_tab_nav"><a data-toggle="tab" href="#ingress">{{ lang._('Ingress Rules') }}</a></li>
</ul>

<div class="tab-content content-box">
    <div id="settings" class="tab-pane fade in active">
        {{ partial("layout_partials/base_form", ['fields': generalForm, 'id': 'frm_settings']) }}
    </div>

    <div id="ingress" class="tab-pane fade in">
        <div class="content-box-main">
            <div id="ingressModeHint" class="alert alert-info" role="alert" style="margin-bottom: 15px; display: none;">
                {{ lang._('Ingress rules are only used in Config File mode.') }}
                {{ lang._('Switch the tunnel mode to Config File if you want the plugin to generate a local cloudflared config and manage origin mappings here.') }}
            </div>

            <div id="ingressRulesPanel" style="display: none;">
                <p class="help-block" style="margin-bottom: 15px;">
                    {{ lang._('Ingress rules map public Cloudflare hostnames to local origin services such as HTTP apps, TCP services, SSH, or RDP targets.') }}
                    {{ lang._('These rules are written into the generated cloudflared config file when you apply settings in Config File mode.') }}
                </p>

                <div class="table-responsive">
                    <table class="table table-striped table-condensed">
                        <thead>
                            <tr>
                                <th>{{ lang._('Enabled') }}</th>
                                <th>{{ lang._('Hostname') }}</th>
                                <th>{{ lang._('Service Type') }}</th>
                                <th>{{ lang._('Origin URL') }}</th>
                                <th>{{ lang._('Actions') }}</th>
                            </tr>
                        </thead>
                        <tbody id="tunnelRulesBody">
                            <tr>
                                <td colspan="5" class="text-muted">
                                    {{ lang._('Loading ingress rules...') }}
                                </td>
                            </tr>
                        </tbody>
                    </table>
                </div>

                <div style="margin-top: 15px;">
                    <button class="btn btn-primary" type="button" onclick="addTunnel()">
                        <i class="fa fa-plus"></i> {{ lang._('Add Ingress Rule') }}
                    </button>
                </div>
            </div>
        </div>
    </div>
</div>

{{ partial('layout_partials/base_apply_button', {
    'button_id': 'reconfigureAct',
    'data_endpoint': '/api/cloudflared/service/reconfigure',
    'data_change_message_content': lang._('After changing Cloudflared settings, please apply them to rewrite the runtime files and restart the service.')
}) }}

<div class="modal fade" id="DialogTunnel" tabindex="-1" role="dialog">
    <div class="modal-dialog modal-lg" role="document">
        <div class="modal-content">
            <div class="modal-header">
                <button type="button" class="close" data-dismiss="modal" aria-label="Close">
                    <span aria-hidden="true">&times;</span>
                </button>
                <h4 class="modal-title">{{ lang._('Edit Ingress Rule') }}</h4>
            </div>
            <div class="modal-body">
                {{ partial('layout_partials/base_form', ['fields': tunnelForm, 'id': 'frm_tunnel']) }}
            </div>
            <div class="modal-footer">
                <button type="button" class="btn btn-default" data-dismiss="modal">
                    {{ lang._('Cancel') }}
                </button>
                <button type="button" class="btn btn-primary" onclick="saveTunnel()">
                    {{ lang._('Save') }}
                </button>
            </div>
        </div>
    </div>
</div>
