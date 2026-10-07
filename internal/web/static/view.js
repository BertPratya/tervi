function shown(value) {
  return typeof value === 'string' && value.length > 0 ? value : 'unknown';
}

export function view(state) {
  const answer = typeof state === 'string' ? { status: state } : (state ?? {});
  if (answer.error === 'invalid_link' || answer.status === 'invalid_link') {
    return {
      message: '✗ Invalid link.',
      name: 'unknown',
      os: 'unknown unknown',
      notice: '',
      buttons: [],
      code: '',
      poll: false,
      error: true,
    };
  }

  const hostname = shown(answer.hostname);
  const osName = shown(answer.os_name);
  const osVersion = shown(answer.os_version);
  const result = {
    message: 'Loading…',
    name: hostname,
    os: `${osName} ${osVersion}`,
    notice: '',
    buttons: [],
    code: '',
    poll: true,
    error: false,
  };

  switch (answer.status) {
    case 'waiting_for_approval':
      result.message = 'Pair a computer?';
      result.buttons = ['Accept', 'Reject'];
      break;
    case 'waiting_for_code':
      result.message = `Type this code into the terminal of ${hostname}:`;
      result.notice = 'Never give this code to anyone.';
      result.code = shown(answer.pairing_code) === 'unknown' ? '' : answer.pairing_code;
      break;
    case 'finishing':
      result.message = `Code accepted. Finishing on ${hostname}…`;
      break;
    case 'paired': {
      const name = shown(answer.display_name) === 'unknown' ? hostname : answer.display_name;
      result.name = name;
      result.message = `✓ ${name} is paired.`;
      result.notice = 'You can close this tab.';
      result.poll = false;
      break;
    }
    case 'rejected':
      result.message = 'Rejected. This computer was not paired.';
      result.poll = false;
      break;
    case 'expired':
      result.message = '✗ This link has expired. Run tervi pair on the computer again to get a new link.';
      result.poll = false;
      result.error = true;
      break;
    case 'failed':
      result.poll = false;
      result.error = true;
      if (answer.failure_reason === 'wrong_codes') {
        result.message = '✗ Pairing failed: the wrong code was typed 5 times. Run tervi pair on the computer again to start over.';
      } else if (answer.failure_reason === 'not_saved') {
        result.message = `✗ Pairing failed: the computer couldn't save its credential. Check the terminal on ${hostname} for details.`;
      } else if (answer.failure_reason === 'not_confirmed') {
        result.message = "✗ Pairing failed: the computer didn't confirm in time. Run tervi pair on the computer again.";
      } else {
        result.message = '✗ Pairing failed.';
      }
      break;
    default:
      break;
  }

  return result;
}
