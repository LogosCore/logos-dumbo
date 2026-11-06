<?php
// Dumbo page, build __BUILD_ID__.
//
// A bind-call minion (docs: contracts/channel-core-connections/): it only
// answers. Core reaches it through the Dumbo channel, which calls it with
// __METHOD__ and a body of the id the message is sent under (16 bytes) and
// the sealed message; the page answers under the same id. Anything else gets
// a plain 404, as a missing page would.
//
// It keeps nothing between requests: it skips staging, so its session keeps
// the build's keys, and it takes any id its build's keys open a message
// under (the build id at first, its session id after). Tasks run within the
// call that brings them; a file moves a chunk a call, and what the page needs
// to go on (where the file is) travels in the transfer's ctx, which core
// echoes on every ack and chunk it sends for the transfer.
error_reporting(0);
@ini_set('display_errors', '0');

final class DumboBytes__ID8__
{
    public $b;

    public function __construct($b)
    {
        $this->b = $b;
    }
}

// A protocol map: integer keys from 0 up would otherwise encode as an array.
final class DumboMap__ID8__
{
    public $a;

    public function __construct($a)
    {
        $this->a = $a;
    }
}

final class DumboPage__ID8__
{
    const METHOD = '__METHOD__';
    const ENC_KEY = '__ENC_KEY__';
    const MAC_KEY = '__MAC_KEY__';
    const CONN = __CONN__;
    const GREETING = '__GREETING__';
    const VERSION = 'dumbo-page/__VERSION__';
    // Commands, by the numbers the build schema gives them.
    const OP_HELLO = 1;
    const OP_DOWNLOAD = 2;
    const OP_UPLOAD = 3;
    const OP_LS = 4;
    const OP_RM = 5;
    const OP_STREAM_DOWNLOAD = 6;
    const OP_STREAM_UPLOAD = 7;
    const OP_WALK = 8;
    // What one answer carries, sized by the build to what the connection
    // carries (its channel's max_message): a walk's part takes listings up
    // to WALK_BYTES (a folder past it goes in the next call), or for
    // WALK_SECONDS: the rest goes in the calls after.
    const WALK_BYTES = __WALK_BYTES__;
    const WALK_SECONDS = 10;
    // A listing says at most this many entries (partial past it): its
    // result travels in the answer to the call.
    const MAX_ENTRIES = __MAX_ENTRIES__;
    // Entries a listing's piece carries.
    const PIECE = __PIECE__;
    // A file moves in chunks of this much, one a call.
    const CHUNK = __CHUNK__;
    // A call whose at (core's time when it made it) is further than this
    // from the page's clock is refused as stale: a replay, or clocks that
    // disagree (protocol.StaleWindow). The page remembers no message ids.
    const STALE_WINDOW = 600;
    // A transfer's ctx is at most this long (protocol.MaxTransferCtx).
    const MAX_CTX = 1024;
    // How many tasks a call may bring.
    const MAX_TASKS = 4;

    public static function serve()
    {
        if (!isset($_SERVER['REQUEST_METHOD']) || $_SERVER['REQUEST_METHOD'] !== self::METHOD) {
            self::notFound();
        }
        $body = file_get_contents('php://input');
        if (!is_string($body) || strlen($body) < 16 + 64 || strlen($body) > (1 << 20)) {
            self::notFound();
        }
        $id = substr($body, 0, 16);
        $plain = self::open($id, "\x02", substr($body, 16));
        if ($plain === null) {
            self::notFound();
        }
        try {
            $off = 0;
            $msg = self::decode($plain, $off, 0);
        } catch (Exception $e) {
            self::notFound();
        }
        if (!is_array($msg) || !isset($msg[0], $msg[1]) || $msg[0] !== 3 || !($msg[1] instanceof DumboBytes__ID8__) || strlen($msg[1]->b) !== 16) {
            self::notFound();
        }
        $items = isset($msg[3]) && is_array($msg[3]) ? $msg[3] : array();
        $out = array();
        $acks = array();
        // A call too old, or from a clock far from this one: nothing in it
        // runs, and core hears why (sealed, so a replayer learns nothing).
        $now = time();
        $stale = !isset($msg[4]) || !is_int($msg[4]) || abs($now - $msg[4]) > self::STALE_WINDOW;
        if ($stale) {
            $items = array();
            $out[] = self::m(array(0 => 13, 1 => 7, 2 => 'the page\'s time is ' . $now));
        }
        foreach (array_values($items) as $i => $item) {
            if (!is_array($item) || !isset($item[0]) || !is_int($item[0])) {
                $out[] = self::err(1, 'an item is a map with its type', $i);
                continue;
            }
            switch ($item[0]) {
                case 6: // rehello
                    $out[] = self::hello();
                    break;
                case 8: // task
                    if (!isset($item[1]) || !($item[1] instanceof DumboBytes__ID8__) || strlen($item[1]->b) !== 16) {
                        $out[] = self::err(1, 'a task has a 16-byte id', $i);
                        break;
                    }
                    $acks[] = $item[1];
                    $out = array_merge($out, self::run($item));
                    break;
                case 9: // cancel: a walk ends where it stood, a transfer stops (an upload's partial file removed); others ended already
                    $out = array_merge($out, self::onCancel($item, $i));
                    break;
                case 15: // resume: go on with a walk
                    $out = array_merge($out, self::onResume($item, $i));
                    break;
                case 11: // ack: a transfer's (with xfer), or core's of results
                    if (isset($item[2])) {
                        $out = array_merge($out, self::onAck($item, $i));
                    }
                    break;
                case 12: // chunk of a file core sends
                    $out = array_merge($out, self::onChunk($item, $i));
                    break;
                case 13: // err: about a transfer, its ctx says what to clean up
                    self::onErr($item);
                    break;
                case 4: // hello_response
                    break;
                default:
                    $out[] = self::err(2, 'a page does not take it', $i);
            }
        }
        if ($acks) {
            array_unshift($out, self::m(array(0 => 11, 1 => $acks)));
        }
        if (!$stale) {
            $out[] = self::m(array(0 => 7, 1 => self::MAX_TASKS)); // get_tasks
        }
        $reply = self::encode(self::m(array(0 => 3, 2 => $msg[1], 3 => $out)));
        $sealed = self::seal($id, "\x01", $reply);
        if ($sealed === null) {
            self::notFound();
        }
        header('Content-Type: application/octet-stream');
        header('Cache-Control: no-store');
        echo $id . $sealed;
        exit;
    }

    private static function notFound()
    {
        http_response_code(404);
        header('Content-Type: text/html; charset=utf-8');
        echo "<!DOCTYPE html>\n<html><head><title>404 Not Found</title></head><body><h1>Not Found</h1>"
            . "<p>The requested URL was not found on this server.</p></body></html>\n";
        exit;
    }

    // run runs a task within the call: what it says now (its result, or a
    // file transfer's first item).
    private static function run($task)
    {
        $cmd = isset($task[2]) ? $task[2] : 0;
        $args = isset($task[3]) && is_array($task[3]) ? $task[3] : array();
        switch ($cmd) {
            case self::OP_HELLO:
                return array(self::done($task[1], true, new DumboBytes__ID8__(self::GREETING . "\n")));
            case self::OP_DOWNLOAD:
                return self::startDownload($task[1], $args);
            case self::OP_UPLOAD:
                return self::startUpload($task[1], $args);
            case self::OP_LS:
                return array(self::ls($task[1], $args));
            case self::OP_RM:
                return array(self::rm($task[1], $args));
            case self::OP_STREAM_DOWNLOAD:
                return self::startStream($task[1], $args);
            case self::OP_STREAM_UPLOAD:
                return self::startUpload($task[1], $args, true);
            case self::OP_WALK:
                return self::startWalk($task[1], $args);
        }
        return array(self::failed($task[1], 'command ' . (is_int($cmd) ? $cmd : '?') . ' is not built into this page'));
    }

    // done is a task's one result, ended: ok, its output and its data (a
    // map, the command's result's shape).
    private static function done($task, $ok, $out = null, $data = null)
    {
        $r = array(0 => 10, 1 => $task, 2 => 0, 3 => true, 4 => $ok);
        if ($out !== null) {
            $r[5] = $out;
        }
        if ($data !== null) {
            $r[7] = $data;
        }
        return self::m($r);
    }

    private static function failed($task, $why)
    {
        $r = array(0 => 10, 1 => $task, 2 => 0, 3 => true, 4 => false, 6 => $why);
        return self::m($r);
    }

    // ls lists a directory (list_dir): its entries in pieces, as the
    // protocol's listings travel, links as links (their own size and time,
    // and their target).
    private static function ls($task, $args)
    {
        $path = isset($args[1]) && is_string($args[1]) && $args[1] !== '' ? $args[1] : '.';
        $abs = @realpath($path);
        if ($abs === false) {
            return self::failed($task, $path . ': no such file or directory');
        }
        if (!@is_dir($abs)) {
            return self::failed($task, $abs . ' is not a directory');
        }
        $l = self::listing($abs, 0);
        if ($l['unread']) {
            $r = array(0 => 10, 1 => $task, 2 => 0, 3 => true, 4 => false, 6 => $abs . ': permission denied', 9 => $l['pieces']);
            return self::m($r);
        }
        return self::m(array(0 => 10, 1 => $task, 2 => 0, 3 => true, 4 => true, 9 => $l['pieces']));
    }

    // listing is a folder's listing (id: the listing's number in its task):
    // its pieces, how many entries, the names of the folders in it (links
    // are not), and whether it could not be read (a piece saying so).
    private static function listing($abs, $id)
    {
        $sep = DIRECTORY_SEPARATOR;
        $parent = dirname($abs);
        if ($parent === $abs) {
            $parent = ''; // a root
        }
        $names = @scandir($abs);
        if ($names === false) {
            $piece = array(1 => $id, 2 => 0, 3 => true, 4 => $abs, 5 => $sep, 9 => 1, 11 => 'permission denied');
            if ($parent !== '') {
                $piece[6] = $parent;
            }
            return array('pieces' => array(self::m($piece)), 'entries' => 0, 'dirs' => array(), 'unread' => true);
        }
        $entries = array();
        $dirs = array();
        $partial = false;
        foreach ($names as $name) {
            if ($name === '.' || $name === '..') {
                continue;
            }
            if (count($entries) >= self::MAX_ENTRIES) {
                $partial = true;
                break;
            }
            $e = self::entry($abs . $sep . $name, $name);
            if ($e[2] === 1) {
                $dirs[] = $name;
            }
            $entries[] = $e;
        }
        // Pieces of PIECE entries, each with the owner names its own
        // entries name, by place from 1.
        $pieces = array();
        $chunks = $entries ? array_chunk($entries, self::PIECE) : array(array());
        foreach ($chunks as $part => $chunk) {
            $owners = array();
            $out = array();
            foreach ($chunk as $e) {
                foreach (array(6 => 'owner', 7 => 'group') as $key => $which) {
                    if (isset($e[$which])) {
                        $at = array_search($e[$which], $owners, true);
                        if ($at === false) {
                            $owners[] = $e[$which];
                            $at = count($owners) - 1;
                        }
                        $e[$key] = $at + 1;
                    }
                    unset($e[$which]);
                }
                $out[] = self::m($e);
            }
            $last = $part === count($chunks) - 1;
            $piece = array(1 => $id, 2 => $part, 3 => $last, 7 => $out);
            if ($part === 0) {
                $piece[4] = $abs;
                $piece[5] = $sep;
                if ($parent !== '') {
                    $piece[6] = $parent;
                }
            }
            if ($last && $partial) {
                $piece[8] = true;
            }
            if ($owners) {
                $piece[10] = $owners;
            }
            $pieces[] = self::m($piece);
        }
        return array('pieces' => $pieces, 'entries' => count($entries), 'dirs' => $dirs, 'unread' => false);
    }

    // A walk (walk_dir) goes depth first, folders in name order, so where
    // it stands is one folder: the last it listed. Each call lists folders
    // while its answer stays within WALK_BYTES and WALK_SECONDS, then hands over a
    // part with the walk's ctx (that folder, its counts and params); core
    // answers with a resume giving it back, and the next call goes on from
    // the folder after it. Links are never followed; depth, hidden and
    // one_fs say which folders it enters, max_folders when it stops.
    private static function startWalk($task, $args)
    {
        $path = isset($args[1]) && is_string($args[1]) ? $args[1] : '';
        if ($path === '') {
            return array(self::failed($task, 'no path'));
        }
        $abs = @realpath($path);
        if ($abs === false || !@is_dir($abs)) {
            return array(self::failed($task, $path . ' is not a directory'));
        }
        $st = @stat($abs);
        $c = array(1 => 'w', 2 => $abs, 3 => $task, 4 => 0, 6 => 0, 7 => 0, 8 => 0, 9 => 0, 10 => 0,
            11 => isset($args[2]) && is_int($args[2]) ? max(0, $args[2]) : 5,
            12 => isset($args[3]) && is_int($args[3]) ? max(0, $args[3]) : 10000,
            13 => !isset($args[4]) || !empty($args[4]),
            14 => !isset($args[5]) || !empty($args[5]),
            15 => $st === false ? 0 : (int) $st['dev']);
        return array(self::walk($c, null));
    }

    // walk lists on from after the folder last (null: the walk's own
    // folder first) and says the part.
    private static function walk($c, $last)
    {
        $start = microtime(true);
        $sep = DIRECTORY_SEPARATOR;
        $root = $c[2];
        $pieces = array();
        $size = 0;
        $cur = $last === null ? '' : self::nextFolder($c, $last);
        $done = false;
        while (true) {
            if ($cur === null || ($c[12] > 0 && $c[7] >= $c[12])) {
                $done = true;
                break;
            }
            if (microtime(true) - $start >= self::WALK_SECONDS) {
                break;
            }
            $abs = $cur === '' ? $root : $root . $sep . $cur;
            $l = self::listing($abs, $c[7]);
            $lsize = 0;
            foreach ($l['pieces'] as $p) {
                $lsize += strlen(self::encode($p));
            }
            // A folder that would take the answer past WALK_BYTES goes in
            // the next call: an answer stays well within a message. One
            // folder alone always goes (5000 entries at most).
            if ($pieces && $size + $lsize > self::WALK_BYTES) {
                break;
            }
            $c[7]++;
            $c[8] += $l['entries'];
            if ($l['unread']) {
                $c[9]++;
            }
            // Folders in it it will not enter: deeper than it goes, hidden
            // (when not asked into them), on another file system.
            foreach ($l['dirs'] as $name) {
                if (!self::enters($c, $cur === '' ? $name : $cur . $sep . $name)) {
                    $c[10]++;
                }
            }
            $pieces = array_merge($pieces, $l['pieces']);
            $size += $lsize;
            $last = $cur;
            $cur = self::nextFolder($c, $cur);
        }
        $data = array(1 => $root, 2 => $c[7], 3 => $c[8]);
        if ($c[9] > 0) {
            $data[4] = $c[9];
        }
        if ($c[10] > 0) {
            $data[5] = $c[10];
        }
        $part = array(0 => 10, 1 => $c[3], 2 => $c[6], 7 => self::m($data));
        if ($pieces) {
            $part[9] = $pieces;
        }
        if ($done) {
            $part[3] = true;
            $part[4] = true;
            return self::m($part);
        }
        $data[6] = $cur === '' ? $root : $root . $sep . $cur;
        $part[7] = self::m($data);
        $c[5] = $last;
        $c[6]++;
        $ctx = self::ctx($c);
        if ($ctx === null) {
            // Where it stands no longer fits a ctx (a path too long): it
            // ends here, said so.
            $part[3] = true;
            $part[4] = false;
            $part[6] = 'the walk stops: a folder\'s path is too long to go on from';
            return self::m($part);
        }
        $part[10] = $ctx;
        return self::m($part);
    }

    // enters: the walk goes into the folder at rel (from its own folder).
    private static function enters($c, $rel)
    {
        $sep = DIRECTORY_SEPARATOR;
        $abs = $c[2] . $sep . $rel;
        if (@is_link($abs) || !@is_dir($abs)) {
            return false;
        }
        if ($c[11] > 0 && substr_count($rel, $sep) + 1 > $c[11]) {
            return false;
        }
        $name = basename($rel);
        if (!$c[13] && $name !== '' && $name[0] === '.') {
            return false;
        }
        if ($c[14]) {
            $st = @lstat($abs);
            if ($st === false || (int) $st['dev'] !== $c[15]) {
                return false;
            }
        }
        return true;
    }

    // nextFolder is the folder after rel in the walk's order: its first
    // folder it enters, else the next of its own folder's, or of the one
    // above; null when none is left.
    private static function nextFolder($c, $rel)
    {
        $sep = DIRECTORY_SEPARATOR;
        foreach (self::subdirs($c, $rel) as $name) {
            $child = $rel === '' ? $name : $rel . $sep . $name;
            if (self::enters($c, $child)) {
                return $child;
            }
        }
        while ($rel !== '') {
            $at = strrpos($rel, $sep);
            $parent = $at === false ? '' : substr($rel, 0, $at);
            $name = $at === false ? $rel : substr($rel, $at + 1);
            foreach (self::subdirs($c, $parent) as $sib) {
                if (strcmp($sib, $name) > 0) {
                    $next = $parent === '' ? $sib : $parent . $sep . $sib;
                    if (self::enters($c, $next)) {
                        return $next;
                    }
                }
            }
            $rel = $parent;
        }
        return null;
    }

    // subdirs are the names in the folder at rel, in order, read once a
    // call: climbing back up asks the same folders again and again.
    private static function subdirs($c, $rel)
    {
        static $seen = array();
        if (!isset($seen[$rel])) {
            $abs = $rel === '' ? $c[2] : $c[2] . DIRECTORY_SEPARATOR . $rel;
            $names = @scandir($abs);
            $seen[$rel] = $names === false ? array() : array_values(array_diff($names, array('.', '..')));
        }
        return $seen[$rel];
    }

    // onResume goes on with a task core gave back where it stood.
    private static function onResume($item, $i)
    {
        $c = self::readCtx($item, 2);
        if ($c === null || $c[1] !== 'w' || !isset($c[5], $c[6]) || !is_string($c[5])) {
            return array(self::err(1, 'a resume the page cannot go on from', $i));
        }
        return array(self::walk($c, $c[5]));
    }

    // onCancel ends a task an operator stopped: a walk, from where it stood;
    // any other ended within its call already.
    private static function onCancel($item, $i)
    {
        $task = isset($item[1]) && $item[1] instanceof DumboBytes__ID8__ && strlen($item[1]->b) === 16 ? $item[1] : null;
        $c = self::readCtx($item, 2);
        if ($c !== null && ($c[1] === 'u' || $c[1] === 'v') && isset($c[5]) && is_string($c[5])) {
            // An upload: what came so far is removed, and the task ends.
            clearstatcache(true, $c[5]);
            $have = @filesize($c[5]);
            @unlink($c[5]);
            $had = $have === false ? 'nothing' : $have . ($c[1] === 'u' ? ' of ' . $c[4] : '') . ' bytes';
            return array(self::failed($c[3], 'stopped: ' . $had . ' had come; the partial file is removed'));
        }
        if ($c !== null && ($c[1] === 'd' || $c[1] === 's')) {
            // A file the page sends: it keeps nothing of it, the task ends.
            return array(self::failed($c[3], 'stopped'));
        }
        if ($c === null || $c[1] !== 'w' || !isset($c[6])) {
            return array(self::err(3, 'no such task: a page runs its tasks within the call', $i, $task));
        }
        $data = array(1 => $c[2], 2 => $c[7], 3 => $c[8]);
        if ($c[9] > 0) {
            $data[4] = $c[9];
        }
        if ($c[10] > 0) {
            $data[5] = $c[10];
        }
        return array(self::m(array(0 => 10, 1 => $c[3], 2 => $c[6], 3 => true, 4 => false, 6 => 'stopped', 7 => self::m($data))));
    }

    // onErr: core refused an item of the page's about a file going to it
    // (an ack asking for a chunk), and its ctx says where the partial file
    // is: removed, as a stop removes it. Core ends the task.
    private static function onErr($item)
    {
        $c = self::readCtx($item, 5);
        if ($c !== null && ($c[1] === 'u' || $c[1] === 'v') && isset($c[5]) && is_string($c[5])) {
            @unlink($c[5]);
        }
    }

    // entry is a directory entry, as a listing's: its owner and group by
    // name for now (ls numbers them by piece).
    private static function entry($full, $name)
    {
        $e = array(1 => $name);
        $st = @lstat($full);
        $link = @is_link($full);
        if ($link) {
            $e[2] = 3;
        } elseif ($st !== false && ($st['mode'] & 0170000) === 0040000) {
            $e[2] = 1;
        } elseif ($st !== false && ($st['mode'] & 0170000) === 0100000) {
            $e[2] = 2;
        } else {
            $e[2] = $st === false ? 2 : 4;
        }
        if ($st !== false) {
            if ($e[2] !== 1) {
                $e[3] = max(0, $st['size']);
            }
            $e[4] = $st['mtime'];
            if ($st['mode'] > 0) {
                $e[5] = $st['mode'];
            }
            if (function_exists('posix_getpwuid')) {
                $pw = @posix_getpwuid($st['uid']);
                $e['owner'] = is_array($pw) && isset($pw['name']) ? $pw['name'] : (string) $st['uid'];
            }
            if (function_exists('posix_getgrgid')) {
                $gr = @posix_getgrgid($st['gid']);
                $e['group'] = is_array($gr) && isset($gr['name']) ? $gr['name'] : (string) $st['gid'];
            }
        }
        if ($link) {
            $target = @readlink($full);
            if (is_string($target)) {
                $e[8] = $target;
            }
        }
        if ($name !== '' && $name[0] === '.') {
            $e[9] = true;
        }
        return $e;
    }

    // rm removes a file, a link (never what it points to) or a folder
    // (delete_path): a folder with what is in it only when recursive says
    // so; never a filesystem's root. The output says what went.
    private static function rm($task, $args)
    {
        $path = isset($args[1]) && is_string($args[1]) ? $args[1] : '';
        $recursive = !empty($args[2]);
        if ($path === '') {
            return self::failed($task, 'no path');
        }
        // Absolute, the last part as it is: a link is removed, not followed.
        $abs = self::absolute($path);
        if (dirname($abs) === $abs) {
            return self::failed($task, $abs . " is a filesystem's root: not removed");
        }
        if (!@is_link($abs) && !@file_exists($abs)) {
            return self::failed($task, $abs . ': no such file or directory');
        }
        $kind = 'file';
        if (@is_link($abs)) {
            $kind = 'link';
            $ok = @unlink($abs);
        } elseif (@is_dir($abs)) {
            $kind = 'folder';
            $names = @scandir($abs);
            $n = $names === false ? 0 : count($names) - 2;
            if (!$recursive && $n > 0) {
                return self::failed($task, $abs . ' is not empty (' . $n . ' entries): remove it with recursive');
            }
            $ok = $recursive ? self::removeTree($abs) : @rmdir($abs);
        } else {
            if (!@is_file($abs)) {
                $kind = 'other';
            }
            $ok = @unlink($abs);
        }
        if (!$ok) {
            $e = error_get_last();
            return self::failed($task, $abs . ': ' . ($e && isset($e['message']) ? preg_replace('/^[a-z_]+\\(.*?\\): /', '', $e['message']) : 'not removed'));
        }
        return self::done($task, true, new DumboBytes__ID8__('removed ' . $abs . ' (' . $kind . ")\n"));
    }

    // absolute is path from the page's working directory, its parts . and
    // .. resolved, its last part not followed.
    private static function absolute($path)
    {
        $sep = DIRECTORY_SEPARATOR;
        $win = $sep === '\\';
        if ($win) {
            $path = str_replace('/', '\\', $path);
        }
        $rooted = $win ? preg_match('/^([A-Za-z]:)?\\\\/', $path) : $path[0] === '/';
        if (!$rooted) {
            $path = getcwd() . $sep . $path;
        }
        $prefix = '';
        if ($win && preg_match('/^[A-Za-z]:/', $path)) {
            $prefix = substr($path, 0, 2);
            $path = substr($path, 2);
        }
        $parts = array();
        foreach (explode($sep, $path) as $part) {
            if ($part === '' || $part === '.') {
                continue;
            }
            if ($part === '..') {
                array_pop($parts);
                continue;
            }
            $parts[] = $part;
        }
        return $prefix . $sep . implode($sep, $parts);
    }

    // removeTree removes a folder and everything in it, links as links.
    private static function removeTree($dir)
    {
        $it = new RecursiveIteratorIterator(
            new RecursiveDirectoryIterator($dir, FilesystemIterator::SKIP_DOTS),
            RecursiveIteratorIterator::CHILD_FIRST
        );
        foreach ($it as $f) {
            $p = $f->getPathname();
            $ok = $f->isDir() && !$f->isLink() ? @rmdir($p) : @unlink($p);
            if (!$ok) {
                return false;
            }
        }
        return @rmdir($dir);
    }

    // A transfer's ctx: what the page needs to go on with it, by number.
    // 1 kind ('d' a file the page sends, 's' one it streams, 'u' one it is
    // sent, 'v' one streamed to it), 2 the file's path, 3 the task, 4 its
    // size (0 for a stream to it: not known); an upload's 5 temporary file
    // and 6 SHA-256 (empty for a stream to it: its last chunk says it).
    // A walk's ('w'): 2 its folder, 3 the task, 4 0, 5 the folder last
    // listed (from its own), 6 the next part's seq, 7 folders, 8 entries,
    // 9 unread, 10 skipped, 11 depth, 12 max_folders, 13 hidden, 14 one_fs,
    // 15 its file system (st_dev).
    private static function ctx($c)
    {
        $b = self::encode(self::m($c));
        return strlen($b) <= self::MAX_CTX ? new DumboBytes__ID8__($b) : null;
    }

    private static function readCtx($item, $key)
    {
        if (!isset($item[$key]) || !($item[$key] instanceof DumboBytes__ID8__)) {
            return null;
        }
        try {
            $off = 0;
            $c = self::decode($item[$key]->b, $off, 0);
        } catch (Exception $e) {
            return null;
        }
        if (!is_array($c) || !isset($c[1], $c[2], $c[3], $c[4]) || !is_string($c[2]) || !($c[3] instanceof DumboBytes__ID8__) || !is_int($c[4])) {
            return null;
        }
        return $c;
    }

    // A file the page sends core (download_file): the first chunk now, the
    // next with each of core's acks, which say where to go on from.
    private static function startDownload($task, $args)
    {
        $path = isset($args[1]) && is_string($args[1]) ? $args[1] : '';
        if ($path === '') {
            return array(self::failed($task, 'no path'));
        }
        $abs = @realpath($path);
        if ($abs === false || !@is_file($abs)) {
            return array(self::failed($task, $path . ' is not a file'));
        }
        if (!@is_readable($abs)) {
            return array(self::failed($task, $abs . ': permission denied'));
        }
        clearstatcache(true, $abs);
        $size = @filesize($abs);
        $ctx = self::ctx(array(1 => 'd', 2 => $abs, 3 => $task, 4 => (int) $size));
        if ($size === false || $ctx === null) {
            return array(self::failed($task, $ctx === null ? 'the path is too long to send' : $abs . ' does not read'));
        }
        $xfer = new DumboBytes__ID8__(random_bytes(16));
        $c = self::fileChunk($xfer, $abs, 0, $size, $ctx);
        if ($c === null) {
            return array(self::failed($task, $abs . ' does not read'));
        }
        $c->a[4] = $size;
        $c->a[6] = $task;
        $c->a[7] = $abs;
        return array($c);
    }

    // fileChunk is the chunk of path from off: the last with the whole's
    // SHA-256. null when it does not read.
    private static function fileChunk($xfer, $path, $off, $size, $ctx)
    {
        $f = @fopen($path, 'rb');
        if ($f === false) {
            return null;
        }
        $data = '';
        $want = min(self::CHUNK, $size - $off);
        if ($want > 0) {
            if (@fseek($f, $off) !== 0) {
                fclose($f);
                return null;
            }
            while (strlen($data) < $want && !feof($f)) {
                $part = fread($f, $want - strlen($data));
                if ($part === false || $part === '') {
                    break;
                }
                $data .= $part;
            }
        }
        fclose($f);
        if (strlen($data) !== $want) {
            return null; // the file shrank
        }
        $c = array(0 => 12, 1 => $xfer, 2 => $off, 3 => new DumboBytes__ID8__($data), 8 => $ctx);
        if ($off + $want === $size) {
            $c[5] = new DumboBytes__ID8__(hash_file('sha256', $path, true));
        }
        return self::m($c);
    }

    // onAck goes on with a transfer core acked: a download's next chunk, or
    // its end once core has every byte.
    private static function onAck($ack, $i)
    {
        $c = self::readCtx($ack, 5);
        if ($c === null || !isset($ack[3]) || !is_int($ack[3])) {
            return array();
        }
        if (!empty($ack[4])) {
            // hold: a stream waits for its downloader, or for its uploader's
            // bytes; core calls again when it may go on.
            return array();
        }
        if ($c[1] === 's') {
            return self::onStreamAck($ack, $c);
        }
        if ($c[1] === 'v') {
            // Bytes came for a stream to the page: it asks for them, from
            // where it has the file.
            clearstatcache(true, $c[5]);
            $have = @filesize($c[5]);
            if ($have === false) {
                return array(); // its task ended
            }
            return array(self::m(array(0 => 11, 2 => $ack[2], 3 => $have, 5 => $ack[5])));
        }
        if ($c[1] !== 'd') {
            return array();
        }
        $xfer = $ack[2];
        $path = $c[2];
        $task = $c[3];
        $size = $c[4];
        $next = $ack[3];
        if ($next >= $size) {
            $sum = @hash_file('sha256', $path, true);
            if ($sum === false) {
                return array(self::failed($task, $path . ' is gone'));
            }
            return array(self::done($task, true, null, self::m(array(1 => $xfer, 2 => $path, 3 => $size, 4 => new DumboBytes__ID8__($sum)))));
        }
        $chunk = self::fileChunk($xfer, $path, $next, $size, $ack[5]);
        if ($chunk === null) {
            return array(self::failed($task, $path . ' changed or is gone while it was sent'));
        }
        return array($chunk);
    }

    // A file the page streams (stream_file): it announces the file, a
    // first chunk without data, and core holds it until a downloader comes;
    // then each of core's acks, sent as the downloader has room, asks for
    // the chunk from where it says.
    private static function startStream($task, $args)
    {
        $path = isset($args[1]) && is_string($args[1]) ? $args[1] : '';
        if ($path === '') {
            return array(self::failed($task, 'no path'));
        }
        $abs = @realpath($path);
        if ($abs === false || !@is_file($abs)) {
            return array(self::failed($task, $path . ' is not a file'));
        }
        if (!@is_readable($abs)) {
            return array(self::failed($task, $abs . ': permission denied'));
        }
        clearstatcache(true, $abs);
        $size = @filesize($abs);
        $ctx = self::ctx(array(1 => 's', 2 => $abs, 3 => $task, 4 => (int) $size));
        if ($size === false || $ctx === null) {
            return array(self::failed($task, $ctx === null ? 'the path is too long to send' : $abs . ' does not read'));
        }
        $xfer = new DumboBytes__ID8__(random_bytes(16));
        return array(self::m(array(0 => 12, 1 => $xfer, 2 => 0, 3 => new DumboBytes__ID8__(''), 4 => $size, 6 => $task, 7 => $abs, 8 => $ctx)));
    }

    // onStreamAck goes on with a stream core acked without hold: the chunk
    // from where it says (from 0, a first chunk again), or the task's end
    // once the downloader has every byte.
    private static function onStreamAck($ack, $c)
    {
        $xfer = $ack[2];
        $path = $c[2];
        $task = $c[3];
        $size = $c[4];
        $next = $ack[3];
        if ($next >= $size) {
            $data = array(1 => $xfer, 2 => $path, 3 => $size);
            $sum = @hash_file('sha256', $path, true);
            if ($sum !== false) {
                $data[4] = new DumboBytes__ID8__($sum);
            }
            return array(self::done($task, true, null, self::m($data)));
        }
        $chunk = self::fileChunk($xfer, $path, $next, $size, $ack[5]);
        if ($chunk === null) {
            return array(self::failed($task, $path . ' changed or is gone while it was streamed'));
        }
        if ($next === 0) {
            $chunk->a[4] = $size;
            $chunk->a[6] = $task;
            $chunk->a[7] = $path;
        }
        return array($chunk);
    }

    // A file core sends the page (upload_file), or streams to it
    // (upload_stream): it gets ready to write it, then asks for the first
    // chunk; each chunk core sends answers its ask. A stream's ask may be
    // answered with hold (no bytes yet): core calls again when they come.
    private static function startUpload($task, $args, $stream = false)
    {
        $offer = isset($args[1]) && is_array($args[1]) ? $args[1] : null;
        if ($offer === null || !isset($offer[1]) || !($offer[1] instanceof DumboBytes__ID8__) || !isset($offer[2]) || !is_string($offer[2])) {
            return array(self::failed($task, 'no file'));
        }
        if (!empty($offer[5]) !== $stream) {
            return array(self::failed($task, $stream ? 'core sent a file where a stream was wanted' : 'core sent a stream where a file was wanted'));
        }
        $size = isset($offer[3]) && is_int($offer[3]) ? $offer[3] : 0;
        $sum = isset($offer[4]) && $offer[4] instanceof DumboBytes__ID8__ ? $offer[4]->b : '';
        $path = isset($args[2]) && is_string($args[2]) ? $args[2] : '';
        $overwrite = !empty($args[3]);
        if ($path === '') {
            return array(self::failed($task, 'no path'));
        }
        // A folder takes the file under its own name.
        $dest = @is_dir($path) ? rtrim($path, '/\\') . DIRECTORY_SEPARATOR . basename($offer[2]) : $path;
        if (@file_exists($dest) && !$overwrite) {
            return array(self::failed($task, $dest . ' exists (overwrite to replace it)'));
        }
        $dir = dirname($dest);
        if (!@is_dir($dir) || !@is_writable($dir)) {
            return array(self::failed($task, $dir . ' is not a folder the page can write to'));
        }
        $tmp = $dir . DIRECTORY_SEPARATOR . '.' . basename($dest) . '.' . bin2hex(substr($offer[1]->b, 0, 4)) . '.part';
        if (@file_put_contents($tmp, '') === false) {
            return array(self::failed($task, $dir . ' is not a folder the page can write to'));
        }
        $ctx = self::ctx(array(1 => $stream ? 'v' : 'u', 2 => $dest, 3 => $task, 4 => $size, 5 => $tmp, 6 => new DumboBytes__ID8__($sum)));
        if ($ctx === null) {
            @unlink($tmp);
            return array(self::failed($task, 'the path is too long'));
        }
        if ($size === 0 && !$stream) {
            return self::placeUpload($offer[1], $ctx, self::readCtx(array(5 => $ctx), 5), $sum, 0);
        }
        return array(self::m(array(0 => 11, 2 => $offer[1], 3 => 0, 5 => $ctx)));
    }

    // onChunk writes a chunk of a file core sends, where the temporary file
    // ends, and asks for the next (or places the file, once whole).
    private static function onChunk($chunk, $i)
    {
        $c = self::readCtx($chunk, 8);
        if ($c === null || ($c[1] !== 'u' && $c[1] !== 'v') || !isset($c[5], $c[6]) || !is_string($c[5])) {
            return array(self::err(1, 'a chunk the page did not ask for', $i));
        }
        $xfer = $chunk[1];
        $tmp = $c[5];
        clearstatcache(true, $tmp);
        $have = @filesize($tmp);
        if ($have === false) {
            return array(self::failed($c[3], 'its temporary file is gone'));
        }
        $off = isset($chunk[2]) && is_int($chunk[2]) ? $chunk[2] : -1;
        $data = isset($chunk[3]) && $chunk[3] instanceof DumboBytes__ID8__ ? $chunk[3]->b : '';
        // In order only: one sent again, or out of order, is written once.
        if ($off === $have && $data !== '') {
            if (@file_put_contents($tmp, $data, FILE_APPEND) !== strlen($data)) {
                @unlink($tmp);
                return array(self::failed($c[3], 'the file does not write'));
            }
            $have += strlen($data);
        }
        if ($c[1] === 'v') {
            // A stream's size and SHA-256 come with its chunks: the last says
            // the SHA-256 of all core passed on.
            $total = isset($chunk[4]) && is_int($chunk[4]) ? $chunk[4] : null;
            $last = isset($chunk[5]) && $chunk[5] instanceof DumboBytes__ID8__ && $off + strlen($data) === $have;
            if (!$last && !($total === 0 && $have === 0)) {
                return array(self::m(array(0 => 11, 2 => $xfer, 3 => $have, 5 => $chunk[8])));
            }
            $sum = isset($chunk[5]) && $chunk[5] instanceof DumboBytes__ID8__ ? $chunk[5]->b : hash('sha256', '', true);
            return self::placeUpload($xfer, $chunk[8], $c, $sum, $have);
        }
        if ($have < $c[4]) {
            return array(self::m(array(0 => 11, 2 => $xfer, 3 => $have, 5 => $chunk[8])));
        }
        return self::placeUpload($xfer, $chunk[8], $c, $c[6]->b, $c[4]);
    }

    // placeUpload checks a whole file core sent and moves it into place: the
    // last ack (it has every byte) and the task's end.
    private static function placeUpload($xfer, $ctxBytes, $c, $want, $size)
    {
        $tmp = $c[5];
        $dest = $c[2];
        $last = self::m(array(0 => 11, 2 => $xfer, 3 => $size, 5 => $ctxBytes));
        $sum = @hash_file('sha256', $tmp, true);
        if ($sum === false || !hash_equals($want, $sum)) {
            @unlink($tmp);
            return array($last, self::failed($c[3], 'the file does not check out (SHA-256)'));
        }
        if (@file_exists($dest) && DIRECTORY_SEPARATOR === '\\') {
            @unlink($dest);
        }
        if (!@rename($tmp, $dest)) {
            @unlink($tmp);
            return array($last, self::failed($c[3], $dest . ' does not write'));
        }
        return array($last, self::done($c[3], true, null, self::m(array(1 => $xfer, 2 => $dest, 3 => $size, 4 => new DumboBytes__ID8__($sum)))));
    }

    // err is an err item about item $item of core's message; an
    // unknown_task names the task, so core ends it.
    private static function err($code, $msg, $item, $task = null)
    {
        $e = array(0 => 13, 1 => $code, 2 => $msg, 3 => $item);
        if ($task !== null) {
            $e[4] = $task;
        }
        return self::m($e);
    }

    private static function m($a)
    {
        return new DumboMap__ID8__($a);
    }

    // hello is the page's host and process, as the protocol has them.
    private static function hello()
    {
        $h = array(0 => 3, 1 => self::pid(), 2 => self::os(), 3 => self::arch());
        $machine = self::machine();
        $h[11] = $machine[0];
        $h[15] = $machine[1];
        $host = function_exists('gethostname') ? @gethostname() : '';
        if (is_string($host) && $host !== '') {
            $h[4] = $host;
        }
        $user = self::user();
        if ($user !== '') {
            $h[5] = $user;
        }
        $h[7] = PHP_SAPI === 'apache2handler' ? 'apache2' : (defined('PHP_BINARY') && PHP_BINARY !== '' ? basename(PHP_BINARY) : PHP_SAPI);
        if (function_exists('posix_geteuid')) {
            $h[8] = posix_geteuid() === 0 ? 5 : 6; // root, user
        }
        if (isset($_SERVER['SERVER_ADDR'])) {
            $addr = @inet_pton($_SERVER['SERVER_ADDR']);
            if (is_string($addr) && (strlen($addr) === 4 || strlen($addr) === 16)) {
                $h[9] = array(new DumboBytes__ID8__($addr));
            }
        }
        $h[10] = self::VERSION;
        $x = array('php' => PHP_VERSION, 'sapi' => PHP_SAPI);
        if (isset($_SERVER['SERVER_SOFTWARE']) && is_string($_SERVER['SERVER_SOFTWARE'])) {
            $x['server'] = substr($_SERVER['SERVER_SOFTWARE'], 0, 200);
        }
        if (isset($_SERVER['SCRIPT_FILENAME']) && is_string($_SERVER['SCRIPT_FILENAME'])) {
            $x['script'] = substr($_SERVER['SCRIPT_FILENAME'], 0, 500);
        }
        $h[12] = $x;
        $h[16] = self::CONN;
        return self::m($h);
    }

    private static function pid()
    {
        $pid = function_exists('getmypid') ? @getmypid() : false;
        return is_int($pid) && $pid > 0 ? $pid : 1;
    }

    // os is Go's name for the host's OS.
    private static function os()
    {
        $family = defined('PHP_OS_FAMILY') ? PHP_OS_FAMILY : PHP_OS;
        switch ($family) {
            case 'Windows':
            case 'WINNT':
                return 'windows';
            case 'Linux':
                return 'linux';
            case 'Darwin':
                return 'darwin';
        }
        $os = strtolower(PHP_OS);
        return $os !== '' ? $os : 'unknown';
    }

    // arch is Go's name for the host's architecture.
    private static function arch()
    {
        $m = function_exists('php_uname') ? strtolower((string) @php_uname('m')) : '';
        switch ($m) {
            case 'x86_64':
            case 'amd64':
                return 'amd64';
            case 'aarch64':
            case 'arm64':
                return 'arm64';
            case 'i386':
            case 'i486':
            case 'i586':
            case 'i686':
            case 'x86':
                return '386';
            case 'armv6l':
            case 'armv7l':
            case 'arm':
                return 'arm';
            case '':
                return PHP_INT_SIZE === 8 ? 'amd64' : '386';
        }
        return preg_replace('/[^a-z0-9_]/', '', $m) ?: 'unknown';
    }

    private static function user()
    {
        if (function_exists('posix_geteuid') && function_exists('posix_getpwuid')) {
            $pw = @posix_getpwuid(posix_geteuid());
            if (is_array($pw) && isset($pw['name']) && $pw['name'] !== '') {
                return $pw['name'];
            }
        }
        $u = getenv('USERNAME');
        if (is_string($u) && $u !== '') {
            return $u;
        }
        $u = getenv('USER');
        return is_string($u) ? $u : '';
    }

    // machine is the machine id: the first source the page can read
    // (docs: Sessions, Machine identity), without running anything.
    private static function machine()
    {
        foreach (array('/etc/machine-id', '/var/lib/dbus/machine-id') as $f) {
            $v = @file_get_contents($f);
            if (is_string($v)) {
                $v = strtolower(trim($v));
                if (preg_match('/^[0-9a-f]{32}$/', $v) && $v !== str_repeat('0', 32)) {
                    return array(4, $v); // linux-machine-id
                }
            }
        }
        $host = function_exists('gethostname') ? @gethostname() : '';
        if (is_string($host)) {
            $host = strtolower(preg_replace('/[\s\x00-\x1f\x7f]+/', '', $host));
            if ($host !== '') {
                return array(8, substr($host, 0, 200)); // hostname
            }
        }
        return array(10, bin2hex(random_bytes(16))); // ephemeral
    }

    // The sealed frame: IV (16) ‖ ver (1) ‖ AES-256-CBC (PKCS#7) ‖ dir (1) ‖
    // HMAC-SHA256 over id ‖ IV ‖ ver ‖ ciphertext ‖ dir.

    private static function open($id, $dir, $frame)
    {
        $n = strlen($frame);
        if ($n < 16 + 1 + 16 + 1 + 32 || ($n - 50) % 16 !== 0) {
            return null;
        }
        $body = substr($frame, 0, $n - 32);
        if ($body[16] !== "\x01" || substr($body, -1) !== $dir) {
            return null;
        }
        $tag = hash_hmac('sha256', $id . $body, base64_decode(self::MAC_KEY), true);
        if (!hash_equals($tag, substr($frame, -32))) {
            return null;
        }
        $plain = openssl_decrypt(substr($body, 17, -1), 'aes-256-cbc', base64_decode(self::ENC_KEY), OPENSSL_RAW_DATA, substr($body, 0, 16));
        return is_string($plain) ? $plain : null;
    }

    private static function seal($id, $dir, $plain)
    {
        $iv = random_bytes(16);
        $ct = openssl_encrypt($plain, 'aes-256-cbc', base64_decode(self::ENC_KEY), OPENSSL_RAW_DATA, $iv);
        if (!is_string($ct)) {
            return null;
        }
        $body = $iv . "\x01" . $ct . $dir;
        return $body . hash_hmac('sha256', $id . $body, base64_decode(self::MAC_KEY), true);
    }

    // CBOR, the subset the protocol uses: integers, byte strings
    // (DumboBytes), text, arrays, maps (DumboMap, or an array that is not a
    // list), true, false, null and floats.

    private static function encode($v)
    {
        if ($v instanceof DumboBytes__ID8__) {
            return self::head(2, strlen($v->b)) . $v->b;
        }
        if ($v instanceof DumboMap__ID8__) {
            $out = '';
            foreach ($v->a as $k => $x) {
                $out .= self::encode($k) . self::encode($x);
            }
            return self::head(5, count($v->a)) . $out;
        }
        if (is_int($v)) {
            return $v >= 0 ? self::head(0, $v) : self::head(1, -1 - $v);
        }
        if (is_string($v)) {
            return self::head(3, strlen($v)) . $v;
        }
        if (is_bool($v)) {
            return $v ? "\xf5" : "\xf4";
        }
        if ($v === null) {
            return "\xf6";
        }
        if (is_float($v)) {
            return "\xfb" . pack('E', $v);
        }
        if (is_array($v)) {
            $out = '';
            if (self::isList($v)) {
                foreach ($v as $x) {
                    $out .= self::encode($x);
                }
                return self::head(4, count($v)) . $out;
            }
            foreach ($v as $k => $x) {
                $out .= self::encode($k) . self::encode($x);
            }
            return self::head(5, count($v)) . $out;
        }
        throw new Exception('cbor: a value it does not encode');
    }

    private static function isList($a)
    {
        $i = 0;
        foreach ($a as $k => $_) {
            if ($k !== $i++) {
                return false;
            }
        }
        return true;
    }

    private static function head($major, $n)
    {
        $m = $major << 5;
        if ($n < 24) {
            return chr($m | $n);
        }
        if ($n < 0x100) {
            return chr($m | 24) . chr($n);
        }
        if ($n < 0x10000) {
            return chr($m | 25) . pack('n', $n);
        }
        if ($n <= 0xffffffff) {
            return chr($m | 26) . pack('N', $n);
        }
        return chr($m | 27) . pack('J', $n);
    }

    private static function decode($s, &$off, $depth)
    {
        if ($depth > 16 || $off >= strlen($s)) {
            throw new Exception('cbor: too deep or too short');
        }
        $ib = ord($s[$off++]);
        $major = $ib >> 5;
        $info = $ib & 0x1f;
        if ($major === 7) {
            switch ($info) {
                case 20:
                    return false;
                case 21:
                    return true;
                case 22:
                case 23:
                    return null;
                case 25:
                    return self::half(self::take($s, $off, 2));
                case 26:
                    $u = unpack('G', self::take($s, $off, 4));
                    return $u[1];
                case 27:
                    $u = unpack('E', self::take($s, $off, 8));
                    return $u[1];
            }
            throw new Exception('cbor: a simple value it does not read');
        }
        $n = self::arg($s, $off, $info);
        switch ($major) {
            case 0:
                return $n;
            case 1:
                return -1 - $n;
            case 2:
                return new DumboBytes__ID8__(self::take($s, $off, $n));
            case 3:
                return self::take($s, $off, $n);
            case 4:
                if ($n > strlen($s) - $off) {
                    throw new Exception('cbor: an array longer than the message');
                }
                $a = array();
                for ($i = 0; $i < $n; $i++) {
                    $a[] = self::decode($s, $off, $depth + 1);
                }
                return $a;
            case 5:
                if ($n > (strlen($s) - $off) / 2) {
                    throw new Exception('cbor: a map longer than the message');
                }
                $a = array();
                for ($i = 0; $i < $n; $i++) {
                    $k = self::decode($s, $off, $depth + 1);
                    if (!is_int($k) && !is_string($k)) {
                        throw new Exception('cbor: a map key it does not take');
                    }
                    $a[$k] = self::decode($s, $off, $depth + 1);
                }
                return $a;
        }
        throw new Exception('cbor: tags are not taken');
    }

    private static function arg($s, &$off, $info)
    {
        if ($info < 24) {
            return $info;
        }
        switch ($info) {
            case 24:
                return ord(self::take($s, $off, 1));
            case 25:
                $u = unpack('n', self::take($s, $off, 2));
                return $u[1];
            case 26:
                $u = unpack('N', self::take($s, $off, 4));
                return $u[1];
            case 27:
                $u = unpack('J', self::take($s, $off, 8));
                if ($u[1] < 0) {
                    throw new Exception('cbor: a number too large');
                }
                return $u[1];
        }
        throw new Exception('cbor: indefinite lengths are not taken');
    }

    private static function take($s, &$off, $n)
    {
        if ($n < 0 || $n > strlen($s) - $off) {
            throw new Exception('cbor: shorter than it says');
        }
        $out = (string) substr($s, $off, $n);
        $off += $n;
        return $out;
    }

    private static function half($b)
    {
        $h = (ord($b[0]) << 8) | ord($b[1]);
        $exp = ($h >> 10) & 0x1f;
        $mant = $h & 0x3ff;
        if ($exp === 0) {
            $v = $mant * pow(2, -24);
        } elseif ($exp === 31) {
            $v = $mant === 0 ? INF : NAN;
        } else {
            $v = ($mant + 1024) * pow(2, $exp - 25);
        }
        return ($h & 0x8000) ? -$v : $v;
    }
}

DumboPage__ID8__::serve();
