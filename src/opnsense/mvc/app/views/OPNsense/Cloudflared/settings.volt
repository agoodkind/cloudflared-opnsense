{#
# Cloudflared Settings View
# Copyright (C) 2025-2026
#}

<script>
    $(document).ready(function () {
        // Toggle visibility of mode-specific fields
        function updateModeVisibility() {
            var mode = $('#general\\.mode').val();
            if (mode === 'token') {
                // Token mode: show token field, hide tunnel ingress section
                $('#row_general\\.token').show();
                $('#tunnelIngressSection').hide();
            } else {
                // Config mode: hide token field, show tunnel ingress section
                $('#row_general\\.token').hide();
                $('#tunnelIngressSection').show();
            }
        }

        // Initial visibility
        mapDataToFormUI({ 'frm_settings': '/api/cloudflared/settings/get' }).done(function () {
            updateModeVisibility();
        });

        // Update on mode change
        $('#general\\.mode').change(updateModeVisibility);

        // Reconfigure after saving
        $("#reconfigureAct").SimpleActionButton({
            onPreAction: function () {
                const dfObj = new $.Deferred();
                saveFormToEndpoint("/api/cloudflared/settings/set", 'frm_settings', function () {
                    dfObj.resolve();
                });
                return dfObj;
            },
            onAction: function (data, status) {
                if (status === "success") {
                    $.ajax({
                        url: '/api/cloudflared/service/reconfigure',
                        type: 'POST',
                        success: function (response) {
                            stdDialogInform(
                                '{{ lang._('Cloudflared') }}',
                                '{{ lang._('Configuration applied successfully') }}',
                                '{{ lang._('OK') }}'
                            );
                        },
                        error: function () {
                            stdDialogInform(
                                '{{ lang._('Error') }}',
                                '{{ lang._('Failed to apply configuration') }}',
                                '{{ lang._('OK') }}'
                            );
                        }
                    });
                }
            }
        });
    });
</script>

<div class="content-box">
    <div class="content-box-main">
        <div class="tab-content">
            <div id="general" class="tab-pane fade in active">
                {{ partial(
                'layout_partials/base_form',
                ['fields': generalForm, 'action': '/ui/cloudflared/settings', 'id': 'frm_settings']
                ) }}
            </div>
        </div>
    </div>
</div>

<div class="content-box" id="tunnelIngressSection" style="display: none;">
    <div class="content-box-header">
        <h3>{{ lang._('Ingress Rules (Config Mode Only)') }}</h3>
    </div>
    <div class="content-box-main">
        <p class="text-muted">
            {{ lang._('Define ingress rules for locally-managed tunnel configuration.') }}
            {{ lang._('Not used in token mode (ingress is managed in Cloudflare dashboard).') }}
        </p>
        <div class="table-responsive">
            <table class="table table-striped table-condensed">
                <thead>
                    <tr>
                        <th>{{ lang._('Hostname') }}</th>
                        <th>{{ lang._('Service') }}</th>
                        <th>{{ lang._('URL') }}</th>
                        <th>{{ lang._('Actions') }}</th>
                    </tr>
                </thead>
                <tbody id="tunnelTableBody">
                    {% for tunnel in tunnels %}
                    <tr data-uuid="{{ tunnel['@uuid'] }}">
                        <td>{{ tunnel.hostname }}</td>
                        <td>{{ tunnel.service }}</td>
                        <td>{{ tunnel.url }}</td>
                        <td>
                            <button class="btn btn-xs btn-default" onclick="editTunnel('{{ tunnel['@uuid'] }}')">
                                <i class="fa fa-edit"></i>
                            </button>
                            <button class="btn btn-xs btn-danger" onclick="deleteTunnel('{{ tunnel['@uuid'] }}')">
                                <i class="fa fa-trash"></i>
                            </button>
                        </td>
                    </tr>
                    {% endfor %}
                </tbody>
            </table>
        </div>
        <div class="col-md-12">
            <button class="btn btn-primary" onclick="addTunnel()">
                <i class="fa fa-plus"></i> {{ lang._('Add Ingress Rule') }}
            </button>
        </div>
    </div>
</div>

<div class="content-box">
    <div class="content-box-main">
        <div class="col-md-12">
            <button id="reconfigureAct" class="btn btn-success">
                <i class="fa fa-check"></i> {{ lang._('Apply Changes') }}
            </button>
        </div>
    </div>
</div>

<script>
    function addTunnel() {
        $('#DialogTunnel .modal-title').text('{{ lang._('Add Ingress Rule') }}');
        $('#frm_tunnel')[0].reset();
        $('#DialogTunnel').data('uuid', '');
        $('#DialogTunnel').modal('show');
    }

    function editTunnel(uuid) {
        $('#DialogTunnel .modal-title').text('{{ lang._('Edit Ingress Rule') }}');
        $.ajax({
            url: '/api/cloudflared/settings/getTunnel/' + uuid,
            type: 'GET',
            success: function (data) {
                if (data.tunnel) {
                    $('#tunnel_hostname').val(data.tunnel.hostname || '');
                    $('#tunnel_service').val(data.tunnel.service || 'http');
                    $('#tunnel_url').val(data.tunnel.url || '');
                    $('#DialogTunnel').data('uuid', uuid);
                    $('#DialogTunnel').modal('show');
                }
            }
        });
    }

    function deleteTunnel(uuid) {
        stdDialogConfirm(
            '{{ lang._('Confirm Delete') }}',
            '{{ lang._('Delete this ingress rule ? ') }}',
            '{{ lang._('Yes') }}',
            '{{ lang._('No') }}',
            function () {
                $.ajax({
                    url: '/api/cloudflared/settings/delTunnel/' + uuid,
                    type: 'POST',
                    success: function () {
                        $('tr[data-uuid="' + uuid + '"]').remove();
                    }
                });
            }
        );
    }

    function saveTunnel() {
        var uuid = $('#DialogTunnel').data('uuid');
        var url = uuid ? '/api/cloudflared/settings/setTunnel/' + uuid
            : '/api/cloudflared/settings/addTunnel';
        saveFormToEndpoint(url, 'frm_tunnel', function () {
            $('#DialogTunnel').modal('hide');
            location.reload();
        });
    }
</script>

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